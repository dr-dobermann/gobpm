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

// InclusiveGateway represents a BPMN inclusive (OR) gateway.
//
// This type implements the inclusive **split** (ADR-005 v.2 §2.9): a diverging
// gateway forks every outgoing flow whose condition is true. It deliberately
// does NOT implement exec.SynchronizingJoin — the inclusive **OR-join** (§2.10,
// the synchronizing merge) is a separate landing (SRD-022); a converging
// Inclusive gateway is unsupported until then.
type InclusiveGateway struct {
	Gateway
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
		},
		nil
}

// Clone returns a per-instance copy of the InclusiveGateway: the embedded
// Gateway is cloned (direction and default flow shared by reference, fresh
// shell). The gateway holds no execution data — condition evaluation reads
// variables through the per-execution environment (ADR-010 §2.4).
func (ig *InclusiveGateway) Clone() flow.Node {
	return &InclusiveGateway{
		Gateway: ig.clone(),
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
	out := ig.Outgoing()

	// Pass-through: a converging merge or a single outgoing continues
	// unconditionally (the OR-join synchronizing merge is SRD-022).
	if len(out) <= 1 {
		return out, nil
	}

	// Diverging: collect the whole true subset (no short-circuit, unlike the
	// Exclusive first-true rule).
	flows := []*flow.SequenceFlow{}

	for _, of := range out {
		// The default flow is the explicit fallback, never a conditional
		// candidate (§13.4.3 evaluates conditions except the default).
		if of == ig.defaultFlow {
			continue
		}

		cond := of.Condition()
		if cond == nil {
			continue // a non-default flow without a condition is never selected
		}

		res, err := ig.checkCondition(ctx, re, cond, of)
		if err != nil {
			return nil, err
		}

		if res {
			flows = append(flows, of)
		}
	}

	if len(flows) == 0 {
		if ig.defaultFlow == nil {
			return nil,
				errs.New(
					errs.M("no available outgoing flow: no condition matched and "+
						"no default"),
					errs.C(errorClass, errs.InvalidState),
					errs.D("inclusive_gateway_id", ig.ID()))
		}

		flows = append(flows, ig.defaultFlow)
	}

	return flows, nil
}

// ----------------------------------------------------------------------------

// interface check
var (
	_ exec.NodeExecutor = (*InclusiveGateway)(nil)
)
