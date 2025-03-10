package value

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/iotaledger/goshimmer/plugins/messagelayer"
	"github.com/iotaledger/goshimmer/tools/integration-tests/tester/framework"
	"github.com/iotaledger/goshimmer/tools/integration-tests/tester/tests"
)

// TestTransactionPersistence issues messages on random peers, restarts them and checks for persistence after restart.
func TestTransactionPersistence(t *testing.T) {
	n, err := f.CreateNetwork("transaction_TestPersistence", 4, 2, framework.CreateNetworkConfig{Faucet: true})
	require.NoError(t, err)
	defer tests.ShutdownNetwork(t, n)

	// wait for peers to change their state to synchronized
	time.Sleep(5 * time.Second)

	// master node sends funds to all peers in the network
	txIdsSlice, addrBalance := tests.SendTransactionFromFaucet(t, n.Peers(), 100)
	txIds := make(map[string]*tests.ExpectedTransaction)
	for _, txID := range txIdsSlice {
		txIds[txID] = nil
	}

	// wait for messages to be gossiped
	time.Sleep(2 * messagelayer.DefaultAverageNetworkDelay)

	// check whether the first issued transaction is available on all nodes, and confirmed
	tests.CheckTransactions(t, n.Peers(), txIds, true, tests.ExpectedInclusionState{
		Confirmed: tests.True(),
	})

	// check ledger state
	tests.CheckBalances(t, n.Peers(), addrBalance)

	// send value message randomly
	randomTxIds := tests.SendTransactionOnRandomPeer(t, n.Peers(), addrBalance, 10, 100)
	for _, randomTxId := range randomTxIds {
		txIds[randomTxId] = nil
	}

	// wait for messages to be gossiped
	time.Sleep(2 * messagelayer.DefaultAverageNetworkDelay)

	// check whether all issued transactions are available on all nodes and confirmed
	tests.CheckTransactions(t, n.Peers(), txIds, true, tests.ExpectedInclusionState{
		Confirmed: tests.True(),
	})

	// check ledger state
	tests.CheckBalances(t, n.Peers(), addrBalance)

	// 3. stop all nodes
	for _, peer := range n.Peers() {
		err = peer.Stop()
		require.NoError(t, err)
	}

	// 4. start all nodes
	for _, peer := range n.Peers() {
		err = peer.Start()
		require.NoError(t, err)
	}

	// wait for peers to start
	time.Sleep(20 * time.Second)

	// check whether all issued transactions are available on all nodes and confirmed
	tests.CheckTransactions(t, n.Peers(), txIds, true, tests.ExpectedInclusionState{
		Confirmed: tests.True(),
	})

	// 5. check ledger state
	tests.CheckBalances(t, n.Peers(), addrBalance)
}

// TestValueColoredPersistence issues colored tokens on random peers, restarts them and checks for persistence after restart.
func TestValueColoredPersistence(t *testing.T) {
	n, err := f.CreateNetwork("valueColor_TestPersistence", 4, 2, framework.CreateNetworkConfig{Faucet: true})
	require.NoError(t, err)
	defer tests.ShutdownNetwork(t, n)

	// wait for peers to change their state to synchronized
	time.Sleep(5 * time.Second)

	// master node sends funds to all peers in the network
	txIdsSlice, addrBalance := tests.SendTransactionFromFaucet(t, n.Peers(), 100)
	txIds := make(map[string]*tests.ExpectedTransaction)
	for _, txID := range txIdsSlice {
		txIds[txID] = nil
	}

	// wait for messages to be gossiped
	time.Sleep(3 * messagelayer.DefaultAverageNetworkDelay)

	// check whether the transactions are available on all nodes, and confirmed
	tests.CheckTransactions(t, n.Peers(), txIds, true, tests.ExpectedInclusionState{
		Confirmed: tests.True(),
	})

	// check ledger state
	tests.CheckBalances(t, n.Peers(), addrBalance)

	// send funds to node 2
	for _, peer := range n.Peers()[1:] {
		fail, txId := tests.SendColoredTransaction(t, peer, n.Peers()[0], addrBalance, tests.TransactionConfig{})
		require.False(t, fail)
		txIds[txId] = nil
	}
	// wait for value messages to be gossiped
	time.Sleep(3 * messagelayer.DefaultAverageNetworkDelay)

	// check whether all issued transactions are persistently available on all nodes, and confirmed
	tests.CheckTransactions(t, n.Peers(), txIds, true, tests.ExpectedInclusionState{
		Confirmed: tests.True(),
	})

	// check ledger state
	tests.CheckBalances(t, n.Peers(), addrBalance)

	// stop all nodes
	for _, peer := range n.Peers() {
		err = peer.Stop()
		require.NoError(t, err)
	}

	// start all nodes
	for _, peer := range n.Peers() {
		err = peer.Start()
		require.NoError(t, err)
	}

	// wait for peers to start
	time.Sleep(20 * time.Second)

	// check whether all issued transactions are persistently available on all nodes, and confirmed
	tests.CheckTransactions(t, n.Peers(), txIds, true, tests.ExpectedInclusionState{
		Confirmed: tests.True(),
	})

	// 5. check ledger state
	tests.CheckBalances(t, n.Peers(), addrBalance)
}
