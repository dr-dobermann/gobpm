package gateways

import (
	"context"
	"sync"

	"github.com/dr-dobermann/gobpm/internal/exec"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/renv"
)

// ParallelGateway represents a BPMN parallel (AND) gateway. Diverging, it
// activates every outgoing flow unconditionally (a fork); converging, it
// synchronizes its incoming flows — it owns its per-instance arrival state and
// serializes concurrent arrivals with its own mutex (ADR-005 §2.4 / ADR-009).
type ParallelGateway struct {
	arrived map[string]string
	Gateway
	mu sync.Mutex
}

// NewParallelGateway creates a new ParallelGateway.
//
// Available options are:
//   - foundation.WithId
//   - foundation.WithDoc
//   - options.WithName
//   - gateways.WithDirection
func NewParallelGateway(opts ...options.Option) (*ParallelGateway, error) {
	g, err := New(opts...)
	if err != nil {
		return nil,
			errs.New(
				errs.M("gate building failed"),
				errs.C(errorClass, errs.BulidingFailed),
				errs.E(err))
	}

	return &ParallelGateway{
			Gateway: *g,
			arrived: map[string]string{},
		},
		nil
}

// Clone returns a per-instance copy of the ParallelGateway: the embedded Gateway
// is cloned (direction and default flow shared by reference, fresh shell, empty
// flows, no container) and the synchronizing-join state (mutex, arrival set)
// starts fresh. See ADR-009.
func (pg *ParallelGateway) Clone() flow.Node {
	return &ParallelGateway{
		Gateway: pg.clone(),
		arrived: map[string]string{},
	}
}

// Arrive records that arrivingTrackID reached the join on incomingFlowID and
// reports whether the join is now complete (every incoming flow has delivered a
// token). It is atomic under the gateway's own mutex, so concurrent track
// arrivals are serialized.
//
// A non-completing arrival is recorded and returns (false, nil). The completing
// arrival returns (true, merged) — the ids of every track absorbed into the
// join (the completing arrival itself is the survivor and is omitted) — and
// clears the arrival state for reuse.
func (pg *ParallelGateway) Arrive(
	incomingFlowID, arrivingTrackID string,
) (complete bool, merged []string) {
	pg.mu.Lock()
	defer pg.mu.Unlock()

	pg.arrived[incomingFlowID] = arrivingTrackID

	if len(pg.arrived) < len(pg.Incoming()) {
		return false, nil
	}

	for _, id := range pg.arrived {
		if id != arrivingTrackID {
			merged = append(merged, id)
		}
	}

	clear(pg.arrived)

	return true, merged
}

// Node returns the gateway as its concrete flow node, so a track reaching it via
// a sequence flow (flow.Target().Node()) dispatches it as the ParallelGateway —
// not the embedded base Gateway, which is not a NodeExecutor.
func (pg *ParallelGateway) Node() flow.Node {
	return pg
}

// Exec activates every outgoing sequence flow of the gateway, unconditionally
// (BPMN 2.0 §13.4.1): no condition evaluation, no default flow, and it cannot
// fail. The same rule drives the diverging split (1->N) and, for a converging
// or mixed gateway, the continuation of the surviving track.
func (pg *ParallelGateway) Exec(
	_ context.Context,
	_ renv.RuntimeEnvironment,
) ([]*flow.SequenceFlow, error) {
	return pg.Outgoing(), nil
}

var (
	_ exec.NodeExecutor      = (*ParallelGateway)(nil)
	_ exec.SynchronizingJoin = (*ParallelGateway)(nil)
)
