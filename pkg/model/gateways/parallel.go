package gateways

import (
	"context"

	"github.com/dr-dobermann/gobpm/internal/exec"
	"github.com/dr-dobermann/gobpm/internal/renv"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// ParallelGateway represents a BPMN parallel (AND) gateway. Diverging, it
// activates every outgoing flow unconditionally (a fork); converging, it
// synchronizes its incoming flows (the join is added in a later milestone).
type ParallelGateway struct {
	Gateway
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
		},
		nil
}

// Clone returns a per-instance copy of the ParallelGateway: the embedded Gateway
// is cloned (direction and default flow shared by reference, fresh shell, empty
// flows, no container). See ADR-009.
func (pg *ParallelGateway) Clone() flow.Node {
	return &ParallelGateway{
		Gateway: pg.clone(),
	}
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

var _ exec.NodeExecutor = (*ParallelGateway)(nil)
