package activities

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/renv"
)

// selectOutgoing applies the BPMN activity-completion rule to the activity's
// outgoing flows (docs/bpmn-spec/semantics/token-flow.md, "Multiple outgoing
// sequence flows on an activity"; SRD-046): an unconditional flow always
// fires, a conditional flow fires when its condition is true, and the default
// flow fires only when no conditional flow fired. A single (or no) outgoing
// flow short-circuits unfiltered — the common case pays nothing. Selecting
// nothing (all flows conditional, all false, no default) is a classified
// error — an engine choice mirroring the gateway rule (the extract prescribes
// the exception for gateways only).
func (a *activity) selectOutgoing(
	ctx context.Context,
	re renv.RuntimeEnvironment,
) ([]*flow.SequenceFlow, error) {
	out := a.Outgoing()
	if len(out) <= 1 {
		return out, nil
	}

	selected := []*flow.SequenceFlow{}
	conditionalFired := false

	for _, of := range out {
		// The default is matched BY ID, not pointer: clone shares defaultFlow
		// by reference while an instance clones its node graph (SRD-046 §4.2).
		if a.defaultFlow != nil && of.ID() == a.defaultFlow.ID() {
			continue // decided after the loop
		}

		cond := of.Condition()
		if cond == nil {
			selected = append(selected, of) // unconditional → always fires

			continue
		}

		ok, err := a.checkCondition(ctx, re, cond, of)
		if err != nil {
			return nil, err
		}

		if ok {
			selected = append(selected, of)
			conditionalFired = true
		}
	}

	if a.defaultFlow != nil && !conditionalFired {
		selected = append(selected, a.defaultFlow)
	}

	if len(selected) == 0 {
		return nil, errs.New(
			errs.M("no outgoing flow selected: all conditions false and "+
				"no default flow"),
			errs.C(errorClass, errs.InvalidState),
			errs.D("activity_id", a.ID()),
			errs.D("activity_name", a.Name()))
	}

	return selected, nil
}

// checkCondition evaluates a sequence flow's boolean condition through the
// engine's ExpressionEngine (reached via the RuntimeEnvironment) — the
// gateways.checkCondition idiom mirrored at the activity (SRD-046 §4.3). A
// non-bool condition and a failed evaluation are classified errors naming the
// activity and the flow.
func (a *activity) checkCondition(
	ctx context.Context,
	re renv.RuntimeEnvironment,
	cond data.FormalExpression,
	of *flow.SequenceFlow,
) (bool, error) {
	if cond.ResultType() != "bool" {
		return false, errs.New(
			errs.M("invalid condition expression type"),
			errs.C(errorClass, errs.TypeCastingError),
			errs.D("outgoing_flow_id", of.ID()),
			errs.D("activity_id", a.ID()))
	}

	res, err := re.ExpressionEngine().Evaluate(ctx, cond, re)
	if err != nil {
		return false, errs.New(
			errs.M("flow condition evaluation failed"),
			errs.C(errorClass, errs.OperationFailed),
			errs.D("outgoing_flow_id", of.ID()),
			errs.D("activity_id", a.ID()),
			errs.E(err))
	}

	return res.Get(ctx).(bool), nil
}
