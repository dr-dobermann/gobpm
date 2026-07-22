package instance

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// throwMIBehavior throws the Multi-Instance's boundary-catchable behavior event for
// one instance completion (ADR-025 §2.8): None on every completion, One on the
// first, Complex per matching condition; All (the default) throws nothing. Called
// off the loop by both decorators (runMISequential / parallelBarrierStep) after the
// drain and BEFORE the activity completes, so the event's boundary fire is enqueued
// while the host boundary is still armed (ADR-025 v.2 §2.12, SRD-056.B FR-6). The
// throw rides Instance.PropagateEvent, which — on the runner goroutine, not the
// loop — blocks only until the loop accepts the event, so the round trip cannot
// self-deadlock. Runs on the runner goroutine.
func (t *track) throwMIBehavior(
	ctx context.Context, mi multiInstance, node flow.Node, completed int,
) error {
	switch mi.Behavior() {
	case activities.BehaviorNone:
		return t.instance.PropagateEvent(ctx, mi.NoneBehaviorEvent())

	case activities.BehaviorOne:
		if completed == 1 {
			return t.instance.PropagateEvent(ctx, mi.OneBehaviorEvent())
		}

	case activities.BehaviorComplex:
		return t.throwComplexBehavior(ctx, mi, node)
	}

	return nil
}

// throwComplexBehavior evaluates each ComplexBehaviorDefinition's condition at the
// host scope and throws the definition's event when it holds — a single completion
// may throw several (ADR-025 §2.8). Runs on the runner goroutine.
func (t *track) throwComplexBehavior(
	ctx context.Context, mi multiInstance, node flow.Node,
) error {
	for _, cbd := range mi.ComplexBehavior() {
		met, err := t.evalBoolAtHost(
			ctx, "behavior condition", node.ID(), cbd.Condition())
		if err != nil {
			return err
		}

		if !met {
			continue
		}

		for _, d := range cbd.Event().Definitions() {
			if err := t.instance.PropagateEvent(ctx, d); err != nil {
				return err
			}
		}
	}

	return nil
}
