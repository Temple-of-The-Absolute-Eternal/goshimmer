package fpc

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sync"
	"time"

	"github.com/iotaledger/hive.go/events"

	"github.com/iotaledger/goshimmer/packages/clock"
	"github.com/iotaledger/goshimmer/packages/vote"
	"github.com/iotaledger/goshimmer/packages/vote/opinion"
)

const (
	toleranceTotalMana = 0.001
)

var (
	// ErrVoteAlreadyOngoing is returned if a vote is already going on for the given ID.
	ErrVoteAlreadyOngoing = errors.New("a vote is already ongoing for the given ID")
	// ErrNoOpinionGiversAvailable is returned if a round cannot be performed as no opinion gives are available.
	ErrNoOpinionGiversAvailable = errors.New("can't perform round as no opinion givers are available")
)

// New creates a new FPC instance.
func New(opinionGiverFunc opinion.OpinionGiverFunc, ownWeightRetrieverFunc opinion.OwnWeightRetriever, paras ...*Parameters) *FPC {
	f := &FPC{
		opinionGiverFunc:       opinionGiverFunc,
		ownWeightRetrieverFunc: ownWeightRetrieverFunc,
		paras:                  DefaultParameters(),
		opinionGiverRng:        rand.New(rand.NewSource(clock.SyncedTime().UnixNano())),
		ctxs:                   make(map[string]*vote.Context),
		queue:                  list.New(),
		queueSet:               make(map[string]struct{}),
		events: vote.Events{
			Finalized:     events.NewEvent(vote.OpinionCaller),
			Failed:        events.NewEvent(vote.OpinionCaller),
			RoundExecuted: events.NewEvent(vote.RoundStatsCaller),
			Error:         events.NewEvent(events.ErrorCaller),
		},
	}
	if len(paras) > 0 {
		f.paras = paras[0]
	}
	return f
}

// FPC is a DRNGRoundBasedVoter which uses the Opinion of other entities
// in order to finalize an Opinion.
type FPC struct {
	events                 vote.Events
	opinionGiverFunc       opinion.OpinionGiverFunc
	ownWeightRetrieverFunc opinion.OwnWeightRetriever
	// the lifo queue of newly enqueued items to vote on.
	queue *list.List
	// contains a set of currently queued items.
	queueSet map[string]struct{}
	queueMu  sync.Mutex
	// contains the set of current vote contexts.
	ctxs   map[string]*vote.Context
	ctxsMu sync.RWMutex
	// parameters to use within FPC.
	paras *Parameters
	// indicates whether the last round was performed successfully.
	lastRoundCompletedSuccessfully bool
	// used to randomly select opinion givers.
	opinionGiverRng *rand.Rand
}

// Vote sets an initial opinion on the vote context and enqueues the vote context.
func (f *FPC) Vote(id string, objectType vote.ObjectType, initOpn opinion.Opinion) error {
	f.queueMu.Lock()
	defer f.queueMu.Unlock()
	f.ctxsMu.RLock()
	defer f.ctxsMu.RUnlock()
	if _, alreadyQueued := f.queueSet[id]; alreadyQueued {
		return fmt.Errorf("%w: %s", ErrVoteAlreadyOngoing, id)
	}
	if _, alreadyOngoing := f.ctxs[id]; alreadyOngoing {
		return fmt.Errorf("%w: %s", ErrVoteAlreadyOngoing, id)
	}
	f.queue.PushBack(vote.NewContext(id, objectType, initOpn))
	f.queueSet[id] = struct{}{}
	return nil
}

// IntermediateOpinion returns the last formed opinion.
// If the vote is not found for the specified ID, it returns with error ErrVotingNotFound.
func (f *FPC) IntermediateOpinion(id string) (opinion.Opinion, error) {
	f.ctxsMu.RLock()
	defer f.ctxsMu.RUnlock()
	voteCtx, has := f.ctxs[id]
	if !has {
		return opinion.Unknown, fmt.Errorf("%w: %s", vote.ErrVotingNotFound, id)
	}
	return voteCtx.LastOpinion(), nil
}

// Events returns the events which happen on a vote.
func (f *FPC) Events() vote.Events {
	return f.events
}

// Round enqueues new items, sets opinions on active vote contexts, finalizes them and then
// queries for opinions.
func (f *FPC) Round(rand float64) error {
	start := time.Now()
	// enqueue new voting contexts
	f.enqueue()
	// we can only form opinions when the last round was actually executed successfully
	if f.lastRoundCompletedSuccessfully {
		// form opinions by using the random number supplied for this new round
		f.formOpinions(rand)
		// clean opinions on vote contexts where an opinion was reached in TotalRoundFinalization
		// number of rounds and clear those who failed to be finalized in MaxRoundsPerVoteContext.
		f.finalizeOpinions()
	}

	// mark a round being done, even though there's no opinion,
	// so this voting context will be cleared eventually
	f.ctxsMu.Lock()
	for voteObjectID := range f.ctxs {
		f.ctxs[voteObjectID].Rounds++
	}
	f.ctxsMu.Unlock()

	// query for opinions on the current vote contexts
	queriedOpinions, err := f.queryOpinions()
	if err == nil {
		f.lastRoundCompletedSuccessfully = true
		// execute a round executed event
		roundStats := &vote.RoundStats{
			Duration:           time.Since(start),
			RandUsed:           rand,
			ActiveVoteContexts: f.ctxs,
			QueriedOpinions:    queriedOpinions,
		}
		// TODO: add possibility to check whether an event handler is registered
		// in order to prevent the collection of the round stats data if not needed
		f.events.RoundExecuted.Trigger(roundStats)
	}

	return err
}

// enqueues items for voting
func (f *FPC) enqueue() {
	f.queueMu.Lock()
	defer f.queueMu.Unlock()
	f.ctxsMu.Lock()
	defer f.ctxsMu.Unlock()
	for ele := f.queue.Front(); ele != nil; ele = f.queue.Front() {
		voteCtx := ele.Value.(*vote.Context)
		f.ctxs[voteCtx.ID] = voteCtx
		f.queue.Remove(ele)
		delete(f.queueSet, voteCtx.ID)
	}
}

// formOpinions updates the opinion for ongoing vote contexts by comparing their liked proportion
// against the threshold appropriate for their given rounds.
func (f *FPC) formOpinions(rand float64) {
	f.ctxsMu.RLock()
	defer f.ctxsMu.RUnlock()
	for _, voteCtx := range f.ctxs {
		// when the vote context is new there's no opinion to form
		if voteCtx.IsNew() {
			continue
		}
		lowerThreshold, upperThreshold := f.setThreshold(voteCtx)

		eta := f.biasTowardsOwnOpinion(voteCtx)

		if eta >= RandUniformThreshold(rand, lowerThreshold, upperThreshold) {
			voteCtx.AddOpinion(opinion.Like)
			continue
		}
		voteCtx.AddOpinion(opinion.Dislike)
	}
}

// emits a Voted event for every finalized vote context (or Failed event if failed) and then removes it from FPC.
func (f *FPC) finalizeOpinions() {
	f.ctxsMu.Lock()
	defer f.ctxsMu.Unlock()
	for id, voteCtx := range f.ctxs {
		if voteCtx.IsFinalized(f.paras.TotalRoundsCoolingOffPeriod, f.paras.TotalRoundsFinalization) {
			f.events.Finalized.Trigger(&vote.OpinionEvent{ID: id, Opinion: voteCtx.LastOpinion(), Ctx: *voteCtx})
			delete(f.ctxs, id)
			continue
		}
		if voteCtx.Rounds >= f.paras.MaxRoundsPerVoteContext {
			f.events.Failed.Trigger(&vote.OpinionEvent{ID: id, Opinion: voteCtx.LastOpinion(), Ctx: *voteCtx})
			delete(f.ctxs, id)
		}
	}
}

// queries the opinions of QuerySampleSize amount of OpinionGivers.
func (f *FPC) queryOpinions() ([]opinion.QueriedOpinions, error) {
	conflictIDs, timestampIDs := f.voteContextIDs()

	// nothing to vote on
	if len(conflictIDs) == 0 && len(timestampIDs) == 0 {
		return nil, nil
	}

	opinionGivers, err := f.opinionGiverFunc()
	if err != nil {
		return nil, err
	}

	// nobody to query
	if len(opinionGivers) == 0 {
		return nil, ErrNoOpinionGiversAvailable
	}

	// select a random subset of opinion givers to query.
	// if the same opinion giver is selected multiple times, we query it only once
	// but use its opinion N selected times.
	opinionGiversToQuery, totalOpinionGiversMana := ManaBasedSampling(opinionGivers, f.paras.MaxQuerySampleSize, f.paras.QuerySampleSize, f.opinionGiverRng)

	// get own mana and calculate total mana
	ownMana, err := f.ownWeightRetrieverFunc()
	if err != nil {
		return nil, err
	}
	totalMana := totalOpinionGiversMana + ownMana

	// create vote Map for existing conflict ids and timestamps
	voteMap := createVoteMapForConflicts(conflictIDs, timestampIDs)
	var voteMapMu sync.Mutex

	// holds queried opinions
	allQueriedOpinions := []opinion.QueriedOpinions{}

	// send queries
	var wg sync.WaitGroup
	for opinionGiverToQuery, selectedCount := range opinionGiversToQuery {
		wg.Add(1)
		go func(opinionGiverToQuery opinion.OpinionGiver, selectedCount int) {
			defer wg.Done()

			queryCtx, cancel := context.WithTimeout(context.Background(), f.paras.QueryTimeout)
			defer cancel()

			// query
			opinions, err := opinionGiverToQuery.Query(queryCtx, conflictIDs, timestampIDs)
			if err != nil || len(opinions) != len(conflictIDs)+len(timestampIDs) {
				// ignore opinions
				return
			}

			queriedOpinions := opinion.QueriedOpinions{
				OpinionGiverID: opinionGiverToQuery.ID().String(),
				Opinions:       make(map[string]opinion.Opinion),
				TimesCounted:   selectedCount,
			}

			// add opinions to vote map
			voteMapMu.Lock()
			defer voteMapMu.Unlock()
			for i, id := range conflictIDs {
				// reuse the opinion N times selected. Note this is always at least 1.
				for j := 0; j < selectedCount; j++ {
					voteMap[id] = append(voteMap[id], opinions[i])
				}
				queriedOpinions.Opinions[id] = opinions[i]
			}
			for i, id := range timestampIDs {
				// reuse the opinion N times selected. Note this is always at least 1.
				for j := 0; j < selectedCount; j++ {
					voteMap[id] = append(voteMap[id], opinions[i])
				}
				queriedOpinions.Opinions[id] = opinions[i]
			}
			allQueriedOpinions = append(allQueriedOpinions, queriedOpinions)
		}(opinionGiverToQuery, selectedCount)
	}
	wg.Wait()

	f.ctxsMu.RLock()
	defer f.ctxsMu.RUnlock()
	// compute liked proportion
	for id, votes := range voteMap {
		var likedSum float64

		votedCount := len(votes)

		for _, o := range votes {
			switch o {
			case opinion.Unknown:
				votedCount--
			case opinion.Like:
				likedSum++
			}
		}

		if votedCount < f.paras.MinOpinionsReceived {
			continue
		}
		f.ctxs[id].Weights = vote.VotingWeights{
			OwnWeight:    ownMana,
			TotalWeights: totalMana,
		}
		f.ctxs[id].ProportionLiked = likedSum / float64(votedCount)
	}

	return allQueriedOpinions, nil
}

func (f *FPC) voteContextIDs() (conflictIDs []string, timestampIDs []string) {
	f.ctxsMu.RLock()
	defer f.ctxsMu.RUnlock()
	for id, ctx := range f.ctxs {
		switch ctx.Type {
		case vote.ConflictType:
			conflictIDs = append(conflictIDs, id)
		case vote.TimestampType:
			timestampIDs = append(timestampIDs, id)
		}
	}
	return conflictIDs, timestampIDs
}

// get round boundaries based on the voting stage
func (f *FPC) setThreshold(voteCtx *vote.Context) (float64, float64) {
	lowerThreshold := f.paras.SubsequentRoundsLowerBoundThreshold
	upperThreshold := f.paras.SubsequentRoundsUpperBoundThreshold

	if voteCtx.HadFirstRound() {
		lowerThreshold = f.paras.FirstRoundLowerBoundThreshold
		upperThreshold = f.paras.FirstRoundUpperBoundThreshold
	}

	if voteCtx.HadFixedRound(f.paras.TotalRoundsCoolingOffPeriod, f.paras.TotalRoundsFinalization, f.paras.TotalRoundsFixedThreshold) {
		lowerThreshold = f.paras.EndingRoundsFixedThreshold
		upperThreshold = f.paras.EndingRoundsFixedThreshold
	}

	return lowerThreshold, upperThreshold
}

// Node biases the received Liked opinion to its current own opinion using base mana proportions
func (f *FPC) biasTowardsOwnOpinion(voteCtx *vote.Context) float64 {
	totalMana := voteCtx.Weights.TotalWeights
	ownMana := voteCtx.Weights.OwnWeight

	if ownMana == 0 || totalMana == 0 {
		return voteCtx.ProportionLiked
	}
	ownOpinion := opinion.ConvertOpinionToFloat64(voteCtx.LastOpinion())
	if ownOpinion < 0 {
		return voteCtx.ProportionLiked
	}
	eta := ownMana/totalMana*ownOpinion + (1-ownMana/totalMana)*voteCtx.ProportionLiked
	return eta
}

// SetOpinionGiverRng sets random number generator in the FPC instance
func (f *FPC) SetOpinionGiverRng(rng *rand.Rand) {
	f.opinionGiverRng = rng
}

// ManaBasedSampling returns list of OpinionGivers to query, weighted by consensus mana and corresponding total mana value.
// If mana not available, fallback to uniform sampling
// weighted random sampling based on https://eli.thegreenplace.net/2010/01/22/weighted-random-generation-in-python/
func ManaBasedSampling(opinionGivers []opinion.OpinionGiver, maxQuerySampleSize, querySampleSize int, rng *rand.Rand) (map[opinion.OpinionGiver]int, float64) {
	totalConsensusMana := 0.0
	totals := make([]float64, 0, len(opinionGivers))

	for i := 0; i < len(opinionGivers); i++ {
		totalConsensusMana += opinionGivers[i].Mana()
		totals = append(totals, totalConsensusMana)
	}

	// check if total mana is almost zero

	if math.Abs(totalConsensusMana) <= toleranceTotalMana {
		// fallback to uniform sampling
		return UniformSampling(opinionGivers, maxQuerySampleSize, querySampleSize, rng), 0
	}

	opinionGiversToQuery := map[opinion.OpinionGiver]int{}
	for i := 0; i < maxQuerySampleSize && len(opinionGiversToQuery) < querySampleSize; i++ {
		rnd := rng.Float64() * totalConsensusMana
		for idx, v := range totals {
			if rnd < v {
				selected := opinionGivers[idx]
				opinionGiversToQuery[selected]++
				break
			}
		}
	}
	return opinionGiversToQuery, totalConsensusMana
}

// UniformSampling returns list of OpinionGivers to query, sampled uniformly
func UniformSampling(opinionGivers []opinion.OpinionGiver, maxQuerySampleSize, querySampleSize int, rng *rand.Rand) map[opinion.OpinionGiver]int {
	opinionGiversToQuery := map[opinion.OpinionGiver]int{}
	for i := 0; i < querySampleSize; i++ {
		selected := opinionGivers[rng.Intn(len(opinionGivers))]
		opinionGiversToQuery[selected]++
	}
	return opinionGiversToQuery
}

// create a voteMap for the stored conflicts and timestamps
func createVoteMapForConflicts(conflictIDs, timestampIDs []string) map[string]opinion.Opinions {
	voteMap := map[string]opinion.Opinions{}

	for _, id := range conflictIDs {
		voteMap[id] = opinion.Opinions{}
	}
	for _, id := range timestampIDs {
		voteMap[id] = opinion.Opinions{}
	}

	return voteMap
}
