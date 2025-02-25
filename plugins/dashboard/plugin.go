package dashboard

import (
	"context"
	"errors"
	"net"
	"net/http"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/iotaledger/hive.go/autopeering/peer/service"
	"github.com/iotaledger/hive.go/crypto/ed25519"
	"github.com/iotaledger/hive.go/daemon"
	"github.com/iotaledger/hive.go/events"
	"github.com/iotaledger/hive.go/logger"
	"github.com/iotaledger/hive.go/node"
	"github.com/labstack/echo"
	"github.com/labstack/echo/middleware"

	"github.com/iotaledger/goshimmer/packages/shutdown"
	"github.com/iotaledger/goshimmer/plugins/autopeering"
	"github.com/iotaledger/goshimmer/plugins/autopeering/local"
	"github.com/iotaledger/goshimmer/plugins/banner"
	"github.com/iotaledger/goshimmer/plugins/config"
	"github.com/iotaledger/goshimmer/plugins/drng"
	"github.com/iotaledger/goshimmer/plugins/gossip"
	"github.com/iotaledger/goshimmer/plugins/messagelayer"
	"github.com/iotaledger/goshimmer/plugins/metrics"
)

// TODO: mana visualization + metrics

// PluginName is the name of the dashboard plugin.
const PluginName = "Dashboard"

var (
	// plugin is the plugin instance of the dashboard plugin.
	plugin *node.Plugin
	once   sync.Once

	log    *logger.Logger
	server *echo.Echo

	nodeStartAt = time.Now()
)

// Plugin gets the plugin instance.
func Plugin() *node.Plugin {
	once.Do(func() {
		plugin = node.NewPlugin(PluginName, node.Enabled, configure, run)
	})
	return plugin
}

func configure(plugin *node.Plugin) {
	log = logger.NewLogger(plugin.Name)
	configureWebSocketWorkerPool()
	configureLiveFeed()
	configureDrngLiveFeed()
	configureVisualizer()
	configureManaFeed()
	configureServer()
}

func configureServer() {
	server = echo.New()
	server.HideBanner = true
	server.HidePort = true
	server.Use(middleware.Recover())

	if config.Node().Bool(CfgBasicAuthEnabled) {
		server.Use(middleware.BasicAuth(func(username, password string, c echo.Context) (bool, error) {
			if username == config.Node().String(CfgBasicAuthUsername) &&
				password == config.Node().String(CfgBasicAuthPassword) {
				return true, nil
			}
			return false, nil
		}))
	}

	setupRoutes(server)
}

func run(*node.Plugin) {
	// run message broker
	runWebSocketStreams()
	// run the message live feed
	runLiveFeed()
	// run the visualizer vertex feed
	runVisualizer()
	runManaFeed()
	// run dRNG live feed if dRNG plugin is enabled
	if !node.IsSkipped(drng.Plugin()) {
		runDrngLiveFeed()
	}

	log.Infof("Starting %s ...", PluginName)
	if err := daemon.BackgroundWorker(PluginName, worker, shutdown.PriorityAnalysis); err != nil {
		log.Panicf("Error starting as daemon: %s", err)
	}
}

func worker(shutdownSignal <-chan struct{}) {
	defer log.Infof("Stopping %s ... done", PluginName)

	// start the web socket worker pool
	wsSendWorkerPool.Start()
	defer wsSendWorkerPool.Stop()

	// submit the mps to the worker pool when triggered
	notifyStatus := events.NewClosure(func(mps uint64) { wsSendWorkerPool.TrySubmit(mps) })
	metrics.Events.ReceivedMPSUpdated.Attach(notifyStatus)
	defer metrics.Events.ReceivedMPSUpdated.Detach(notifyStatus)

	stopped := make(chan struct{})
	bindAddr := config.Node().String(CfgBindAddress)
	go func() {
		log.Infof("%s started, bind-address=%s, basic-auth=%v", PluginName, bindAddr, config.Node().Bool(CfgBasicAuthEnabled))
		if err := server.Start(bindAddr); err != nil {
			if !errors.Is(err, http.ErrServerClosed) {
				log.Errorf("Error serving: %s", err)
			}
			close(stopped)
		}
	}()

	// stop if we are shutting down or the server could not be started
	select {
	case <-shutdownSignal:
	case <-stopped:
	}

	log.Infof("Stopping %s ...", PluginName)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Errorf("Error stopping: %s", err)
	}
}

const (
	// MsgTypeNodeStatus is the type of the NodeStatus message.
	MsgTypeNodeStatus byte = iota
	// MsgTypeMPSMetric is the type of the message per second (MPS) metric message.
	MsgTypeMPSMetric
	// MsgTypeMessage is the type of the message.
	MsgTypeMessage
	// MsgTypeNeighborMetric is the type of the NeighborMetric message.
	MsgTypeNeighborMetric
	// MsgTypeComponentCounterMetric is the type of the component counter triggered per second.
	MsgTypeComponentCounterMetric
	// MsgTypeDrng is the type of the dRNG message.
	MsgTypeDrng
	// MsgTypeTipsMetric is the type of the TipsMetric message.
	MsgTypeTipsMetric
	// MsgTypeVertex defines a vertex message.
	MsgTypeVertex
	// MsgTypeTipInfo defines a tip info message.
	MsgTypeTipInfo
	// MsgTypeManaValue defines a mana value message.
	MsgTypeManaValue
	// MsgTypeManaMapOverall defines a message containing overall mana map.
	MsgTypeManaMapOverall
	// MsgTypeManaMapOnline defines a message containing online mana map.
	MsgTypeManaMapOnline
	// MsgTypeManaAllowedPledge defines a message containing a list of allowed mana pledge nodeIDs.
	MsgTypeManaAllowedPledge
	// MsgTypeManaPledge defines a message that is sent when mana was pledged to the node.
	MsgTypeManaPledge
	// MsgTypeManaInitPledge defines a message that is sent when initial pledge events are sent to the dashboard.
	MsgTypeManaInitPledge
	// MsgTypeManaRevoke defines a message that is sent when mana was revoked from a node.
	MsgTypeManaRevoke
	// MsgTypeManaInitRevoke defines a message that is sent when initial revoke events are sent to the dashboard.
	MsgTypeManaInitRevoke
	// MsgTypeManaInitDone defines a message that is sent when all initial values are sent.
	MsgTypeManaInitDone
	// MsgManaDashboardAddress is the socket address of the dashboard to stream mana from.
	MsgManaDashboardAddress
	// MsgTypeMsgOpinionFormed defines a tip info message.
	MsgTypeMsgOpinionFormed
)

type wsmsg struct {
	Type byte        `json:"type"`
	Data interface{} `json:"data"`
}

type msg struct {
	ID          string `json:"id"`
	Value       int64  `json:"value"`
	PayloadType uint32 `json:"payload_type"`
}

type nodestatus struct {
	ID      string            `json:"id"`
	Version string            `json:"version"`
	Uptime  int64             `json:"uptime"`
	Synced  bool              `json:"synced"`
	Beacons map[string]Beacon `json:"beacons"`
	Mem     *memmetrics       `json:"mem"`
}

// Beacon contains a sync beacons detailed status.
type Beacon struct {
	MsgID    string `json:"msg_id"`
	SentTime int64  `json:"sent_time"`
	Synced   bool   `json:"synced"`
}

type memmetrics struct {
	HeapSys      uint64 `json:"heap_sys"`
	HeapAlloc    uint64 `json:"heap_alloc"`
	HeapIdle     uint64 `json:"heap_idle"`
	HeapReleased uint64 `json:"heap_released"`
	HeapObjects  uint64 `json:"heap_objects"`
	NumGC        uint32 `json:"num_gc"`
	LastPauseGC  uint64 `json:"last_pause_gc"`
}

type neighbormetric struct {
	ID               string `json:"id"`
	Address          string `json:"address"`
	ConnectionOrigin string `json:"connection_origin"`
	BytesRead        uint64 `json:"bytes_read"`
	BytesWritten     uint64 `json:"bytes_written"`
}

type componentsmetric struct {
	Store      uint64 `json:"store"`
	Solidifier uint64 `json:"solidifier"`
	Scheduler  uint64 `json:"scheduler"`
	Booker     uint64 `json:"booker"`
}

func neighborMetrics() []neighbormetric {
	var stats []neighbormetric

	// gossip plugin might be disabled
	neighbors := gossip.Manager().AllNeighbors()
	if neighbors == nil {
		return stats
	}

	for _, neighbor := range neighbors {
		// unfortunately the neighbor manager doesn't keep track of the origin of the connection
		origin := "Inbound"
		for _, peer := range autopeering.Selection().GetOutgoingNeighbors() {
			if neighbor.Peer == peer {
				origin = "Outbound"
				break
			}
		}

		host := neighbor.Peer.IP().String()
		port := neighbor.Peer.Services().Get(service.GossipKey).Port()
		stats = append(stats, neighbormetric{
			ID:               neighbor.Peer.ID().String(),
			Address:          net.JoinHostPort(host, strconv.Itoa(port)),
			BytesRead:        neighbor.BytesRead(),
			BytesWritten:     neighbor.BytesWritten(),
			ConnectionOrigin: origin,
		})
	}
	return stats
}

func currentNodeStatus() *nodestatus {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	status := &nodestatus{
		Beacons: make(map[string]Beacon),
	}
	status.ID = local.GetInstance().ID().String()

	// node status
	status.Version = banner.AppVersion
	status.Uptime = time.Since(nodeStartAt).Milliseconds()

	var beacons map[ed25519.PublicKey]messagelayer.Status
	status.Synced, beacons = messagelayer.SyncStatus()

	for publicKey, s := range beacons {
		status.Beacons[publicKey.String()] = Beacon{
			MsgID:    s.MsgID.Base58(),
			SentTime: s.SentTime,
			Synced:   s.Synced,
		}
	}

	// memory metrics
	status.Mem = &memmetrics{
		HeapSys:      m.HeapSys,
		HeapAlloc:    m.HeapAlloc,
		HeapIdle:     m.HeapIdle,
		HeapReleased: m.HeapReleased,
		HeapObjects:  m.HeapObjects,
		NumGC:        m.NumGC,
		LastPauseGC:  m.PauseNs[(m.NumGC+255)%256],
	}
	return status
}
