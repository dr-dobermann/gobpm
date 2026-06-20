// Package gateways provides BPMN gateway implementations.
package gateways

import (
	"context"
	"sync"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/exec"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/renv"
)

// InclusiveGateway represents a BPMN inclusive (OR) gateway.
//
// Diverging, it implements the inclusive **split** (ADR-005 v.2 §2.9): it forks
// every outgoing flow whose condition is true. Converging, it is the inclusive
// **OR-join** (§2.10) — a reachability-based exec.ReachabilityJoin: it owns its
// per-instance arrival state under its own mutex (ADR-005 §2.4 / ADR-009), and
// the instance loop supplies reachability via an exec.FlowChecker, re-checking
// the join when a token parks at it and on every token death. Completion is
// count-only on Arrive (every incoming flow marked) or reachability-based on
// Recheck (no live token can still reach an un-marked incoming flow).
type InclusiveGateway struct {
	order   []string
	arrived map[string]string
	Gateway
	mu    sync.Mutex
	fired bool
}

// NewInclusiveGateway creates a new InclusiveGateway.
//
// Available options are:
//   - foundation.WithId
//   - foundation.WithDoc
//   - options.WithName
//   - gateways.WithDirection
func NewInclusiveGateway(opts ...options.Option) (*InclusiveGateway, error) {
	g, err := New(opts...)
	if err != nil {
		return nil,
			errs.New(
				errs.M("gate building failed"),
				errs.C(errorClass, errs.BulidingFailed),
				errs.E(err))
	}

	return &InclusiveGateway{
			Gateway: *g,
			arrived: map[string]string{},
		},
		nil
}

// Clone returns a per-instance copy of the InclusiveGateway: the embedded
// Gateway is cloned (direction and default flow shared by reference, fresh
// shell) and the synchronizing-join state (mutex, arrival table, order log,
// fired flag) starts fresh (ADR-009). Condition evaluation reads variables
// through the per-execution environment (ADR-010 §2.4).
func (ig *InclusiveGateway) Clone() flow.Node {
	return &InclusiveGateway{
		Gateway: ig.clone(),
		arrived: map[string]string{},
	}
}

// Node returns the gateway as its concrete flow node, so a track reaching it via
// a sequence flow dispatches it as the InclusiveGateway — not the embedded base
// Gateway, which is not a NodeExecutor.
func (ig *InclusiveGateway) Node() flow.Node {
	return ig
}

// Exec routes the arriving token through the inclusive split (ADR-005 v.2 §2.9,
// BPMN §13.4.3):
//
//   - A converging merge / single outgoing flow is a non-synchronizing
//     pass-through — the outgoing flow is returned unconditionally. (The
//     synchronizing OR-join is SRD-022; until then a converging Inclusive
//     gateway does not synchronize.)
//   - A diverging gateway returns EVERY outgoing flow whose condition is true
//     (the true subset, ≥1) — the default flow is excluded as the fallback, a
//     conditionless non-default flow is never selected.
//   - When no condition matches, the default flow is taken; when none matches
//     and there is no default, the instance fails (an unroutable token is a
//     modeling error).
func (ig *InclusiveGateway) Exec(
	ctx context.Context,
	re renv.RuntimeEnvironment,
) ([]*flow.SequenceFlow, error) {
	// The inclusive split is the shared true-subset fork (§2.9); the OR-join
	// synchronizing merge is handled by Arrive/Recheck (SRD-022), not Exec.
	return ig.forkTrueSubset(ctx, re)
}

// Arrive records that arrivingTrackID reached the join on incomingFlowID and
// reports completion via the **count fast path** only: the join completes when
// every incoming flow has delivered a token (the live arriving track is then the
// survivor — last-in — and merged holds the other arrived tracks). A subset
// arrival is recorded and parks (returns false, nil); the reachability decision
// for a subset is the loop's, via Recheck. Atomic under the gateway's own mutex.
func (ig *InclusiveGateway) Arrive(
	incomingFlowID, arrivingTrackID string,
) (complete bool, merged []string) {
	ig.mu.Lock()
	defer ig.mu.Unlock()

	if ig.fired {
		return false, nil
	}

	if _, seen := ig.arrived[incomingFlowID]; !seen {
		ig.arrived[incomingFlowID] = arrivingTrackID
		ig.order = append(ig.order, arrivingTrackID)
	}

	if len(ig.arrived) < len(ig.Incoming()) {
		return false, nil
	}

	ig.fired = true

	return true, absorb(ig.order, arrivingTrackID)
}

// Recheck re-evaluates a parked OR-join without a new arrival (the loop calls it
// when a token parks at the join and on every token death). It asks fc whether
// any un-marked incoming flow is still reachable; when none is — and at least one
// token has arrived — the join fires with the earliest arrival as survivor
// (first-in) and the rest merged. A reachability error is treated conservatively
// (not complete — wait). Atomic under the gateway's own mutex.
func (ig *InclusiveGateway) Recheck(
	fc exec.FlowChecker,
) (complete bool, survivor string, merged []string) {
	ig.mu.Lock()
	defer ig.mu.Unlock()

	if ig.fired || len(ig.order) == 0 {
		return false, "", nil
	}

	if unmarked := ig.unmarkedFlows(); len(unmarked) > 0 {
		reachable, err := fc.CheckFlows(ig, unmarked)
		if err != nil || len(reachable) > 0 {
			return false, "", nil
		}
	}

	ig.fired = true
	survivor = ig.order[0]

	return true, survivor, absorb(ig.order, survivor)
}

// unmarkedFlows returns the incoming flows that have not yet delivered a token.
func (ig *InclusiveGateway) unmarkedFlows() []*flow.SequenceFlow {
	var unmarked []*flow.SequenceFlow

	for _, in := range ig.Incoming() {
		if _, marked := ig.arrived[in.ID()]; !marked {
			unmarked = append(unmarked, in)
		}
	}

	return unmarked
}

// absorb returns every track id in order except the survivor.
func absorb(order []string, survivor string) []string {
	var merged []string

	for _, id := range order {
		if id != survivor {
			merged = append(merged, id)
		}
	}

	return merged
}

// ----------------------------------------------------------------------------

// interface check
var (
	_ exec.ReachabilityJoin = (*InclusiveGateway)(nil)
)
