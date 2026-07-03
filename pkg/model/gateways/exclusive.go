// Package gateways provides BPMN gateway implementations.
package gateways

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/exec"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/renv"
)

// ExclusiveGateway represents a BPMN exclusive gateway.
type ExclusiveGateway struct {
	Gateway
}

// NewExclusiveGateway creates a new ExclusiveGateway.
//
// Available options are:
//   - foundation.WithID
//   - foundation.WithDoc
//   - options.WithName
//   - gateways.WithDirection
func NewExclusiveGateway(opts ...options.Option) (*ExclusiveGateway, error) {
	g, err := New(opts...)
	if err != nil {
		return nil,
			errs.New(
				errs.M("gate building failed"),
				errs.C(errorClass, errs.BulidingFailed),
				errs.E(err))
	}

	return &ExclusiveGateway{
			Gateway: *g,
		},
		nil
}

// Clone returns a per-instance copy of the ExclusiveGateway: the embedded
// Gateway is cloned (direction and default flow shared by reference, fresh
// shell, empty flows, no container). The gateway holds no execution data —
// condition evaluation reads variables through the per-execution environment
// (ADR-010 §2.4).
func (eg *ExclusiveGateway) Clone() (flow.Node, error) {
	return &ExclusiveGateway{
		Gateway: eg.clone(),
	}, nil
}

// Node returns the gateway as its concrete flow node, so a track reaching it via
// a sequence flow (flow.Target().Node()) dispatches it as the ExclusiveGateway —
// not the embedded base Gateway, which is not a NodeExecutor.
func (eg *ExclusiveGateway) Node() flow.Node {
	return eg
}

// Exec routes the arriving token (ADR-005 v.2 §2.8, BPMN §13.4.2):
//
//   - A converging merge / single outgoing flow is a non-synchronizing
//     pass-through (§2.3) — the outgoing flow is returned unconditionally.
//   - A diverging gateway takes the FIRST outgoing flow whose condition
//     evaluates true and stops (short-circuit); a conditionless non-default
//     flow is never selected.
//   - When no condition matches, the default flow is taken; when none matches
//     and there is no default, the instance fails (an unroutable token is a
//     modeling error).
func (eg *ExclusiveGateway) Exec(
	ctx context.Context,
	re renv.RuntimeEnvironment,
) ([]*flow.SequenceFlow, error) {
	out := eg.Outgoing()

	// Pass-through: a converging merge or a single outgoing continues
	// unconditionally (non-synchronizing Exclusive merge, §2.3).
	if len(out) <= 1 {
		return out, nil
	}

	// Diverging: first-true wins, short-circuit (§13.4.2).
	for _, of := range out {
		// The default flow is the explicit fallback, never a conditional
		// candidate — exclude it regardless of any condition (§13.4.2:
		// "conditions on outgoing flows … except the default").
		if of == eg.defaultFlow {
			continue
		}

		cond := of.Condition()
		if cond == nil {
			continue // a non-default flow without a condition is never selected
		}

		res, err := eg.checkCondition(ctx, re, cond, of)
		if err != nil {
			return nil, err
		}

		if res {
			return []*flow.SequenceFlow{of}, nil
		}
	}

	if eg.defaultFlow == nil {
		return nil,
			errs.New(
				errs.M("no available outgoing flow: no condition matched and "+
					"no default"),
				errs.C(errorClass, errs.InvalidState),
				errs.D("exclusive_gateway_id", eg.ID()))
	}

	return []*flow.SequenceFlow{eg.defaultFlow}, nil
}

// ----------------------------------------------------------------------------

// interface check
var (
	_ exec.NodeExecutor = (*ExclusiveGateway)(nil)
)
