package instance

import (
	"context"

	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// bindMICounters publishes the §2.9 runtime attributes at the host scope, shared
// by the sequential and parallel completion paths (SRD-056.B FR-7): the current
// counts a completionCondition, a Complex behavior condition, and the body
// resolve by name.
func (ls *loopState) bindMICounters(
	hostScope scope.DataPath, n, active, completed, terminated int,
) error {
	binds := []miBinding{
		{name: "numberOfInstances", value: n},
		{name: "numberOfActiveInstances", value: active},
		{name: "numberOfCompletedInstances", value: completed},
		{name: "numberOfTerminatedInstances", value: terminated},
	}

	for _, b := range binds {
		if err := ls.inst.sc.bindDataItemAt(hostScope, b.name, b.value); err != nil {
			return err
		}
	}

	return nil
}

// throwMIBehavior throws the Multi-Instance's boundary-catchable behavior event
// for one instance completion (ADR-025 §2.8): None on every completion, One on
// the first, Complex per matching condition; All (the default) throws nothing.
// Called from both completion paths BEFORE the host resumes, so the event's
// boundary fire is enqueued while the host boundary is still armed (FR-6). Runs
// on the loop goroutine.
func (ls *loopState) throwMIBehavior(
	ctx context.Context, mi multiInstance, host *track, node flow.Node,
	completed int,
) error {
	switch mi.Behavior() {
	case activities.BehaviorNone:
		return ls.inst.PropagateEvent(ctx, mi.NoneBehaviorEvent())

	case activities.BehaviorOne:
		if completed == 1 {
			return ls.inst.PropagateEvent(ctx, mi.OneBehaviorEvent())
		}

	case activities.BehaviorComplex:
		return ls.throwComplexBehavior(ctx, mi, host, node)
	}

	return nil
}

// throwComplexBehavior evaluates each ComplexBehaviorDefinition's condition at
// the host scope and throws the definition's event when it holds — a single
// completion may throw several (ADR-025 §2.8).
func (ls *loopState) throwComplexBehavior(
	ctx context.Context, mi multiInstance, host *track, node flow.Node,
) error {
	for _, cbd := range mi.ComplexBehavior() {
		met, err := evalBoolAtHost(
			ctx, host, "mi-behavior", node.ID(), cbd.Condition())
		if err != nil {
			return err
		}

		if !met {
			continue
		}

		for _, d := range cbd.Event().Definitions() {
			if err := ls.inst.PropagateEvent(ctx, d); err != nil {
				return err
			}
		}
	}

	return nil
}
