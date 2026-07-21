package instance

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// standardLoop is the runtime capability a Standard-Loop marker exposes
// (ADR-025 §2.3, BPMN §13.3.6). Recognizing the loop by this capability — not by
// a concrete type — keeps the runtime model-agnostic and naturally excludes a
// Multi-Instance marker, which does not offer these methods.
type standardLoop interface {
	LoopCondition() data.FormalExpression
	TestBefore() bool
	LoopMaximum() (int, bool)
}

// standardLoopOf reports the node's Standard-Loop characteristics, or nil when
// the node runs once (no marker, or a Multi-Instance marker). A composite host
// never reaches this path — it parks before executeNode — so a leaf activity is
// the only node a Standard Loop drives in place (ADR-025 §2.2).
func standardLoopOf(node flow.Node) standardLoop {
	lch, ok := node.(interface {
		LoopCharacteristics() activities.LoopCharacteristics
	})
	if !ok {
		return nil
	}

	sl, ok := lch.LoopCharacteristics().(standardLoop)
	if !ok {
		return nil
	}

	return sl
}

// executeStep runs the current node once, or — when it carries a Standard Loop —
// re-runs it in place until the loop terminates (ADR-025 §2.2, leaf-Task
// mechanism). It returns the outgoing flows to follow exactly once, on loop
// exit.
func (t *track) executeStep(
	ctx context.Context, step *stepInfo,
) ([]*flow.SequenceFlow, error) {
	if sl := standardLoopOf(step.node); sl != nil {
		// a composite (scopeHost) Standard Loop iterates off the loop via the
		// scope decorator (SRD-054 §2.12); a leaf Task iterates in place.
		if _, ok := step.node.(scopeHost); ok {
			return t.runCompositeLoop(ctx, step, sl)
		}

		return t.runStandardLoop(ctx, step, sl)
	}

	// a Multi-Instance composite drives itself off the loop via its own decorator
	// (ADR-025 v.2 §2.12): sequential await-each (runMISequential, SRD-055) or
	// parallel fan-out-then-await-all (runMIParallel, SRD-056.A).
	if mi := multiInstanceOf(step.node); mi != nil {
		if _, ok := step.node.(scopeHost); ok {
			if mi.IsSequential() {
				return t.runMISequential(ctx, step, mi)
			}

			return t.runMIParallel(ctx, step, mi)
		}
	}

	return t.executeNode(ctx, step)
}

// runStandardLoop executes a leaf activity repeatedly while its loopCondition
// holds (BPMN §13.3.6): testBefore selects a pre-tested (while) or post-tested
// (do-while) loop, loopMaximum caps the count, and a 0-based loopCounter is
// published each pass. Each pass re-runs executeNode, which opens a fresh
// execution frame — that per-pass frame IS the iteration isolation (ADR-025
// §2.2), so no scope is needed. The single outgoing flow is followed once, after
// the loop.
func (t *track) runStandardLoop(
	ctx context.Context, step *stepInfo, sl standardLoop,
) ([]*flow.SequenceFlow, error) {
	loopCounter := 0

	var nextFlows []*flow.SequenceFlow

	for {
		// publish the 0-based ordinal so both the condition and the inner
		// activity resolve it by name via scope walk-up (SRD-054 FR-10).
		if err := t.instance.sc.bindLoopCounterAt(
			t.scopePath, loopCounter); err != nil {
			return nil, err
		}

		// A pre-tested (while) loop tests before every pass; a post-tested
		// (do-while) loop skips the test on the first pass only, so both share
		// one test site.
		if sl.TestBefore() || loopCounter > 0 {
			cont, err := t.evalLoopCond(ctx, step.node, sl)
			if err != nil {
				return nil, err
			}

			if !cont {
				break
			}
		}

		// re-arm the step for another execution — finalizeNodeExecution ended
		// the previous pass at StepEnded.
		step.state = StepCreated

		flows, err := t.executeNode(ctx, step)
		if err != nil {
			return nil, err
		}

		nextFlows = flows
		loopCounter++

		if m, ok := sl.LoopMaximum(); ok && loopCounter >= m {
			break
		}
	}

	// A pre-tested loop whose condition is false at entry runs the body zero
	// times, so executeNode produced no flows — but the token still leaves via
	// the activity's outgoing sequence flow (BPMN §13.3.6). A leaf loop activity
	// carries no conditional flow selection, so its declared outgoing flows are
	// the ones to follow.
	if nextFlows == nil {
		nextFlows = step.node.Outgoing()
	}

	return nextFlows, nil
}

// evalLoopCond evaluates the loop's boolean loopCondition against a transient
// frame at the track's scope (the same shape conditional-event evaluation uses),
// where the current loopCounter has already been published. A non-boolean result
// is a modeling error surfaced to the caller.
func (t *track) evalLoopCond(
	ctx context.Context, node flow.Node, sl standardLoop,
) (bool, error) {
	frame, err := t.instance.sc.openFrameAt("std-loop", node.ID(), t.scopePath)
	if err != nil {
		return false, err
	}
	defer frame.Discard()

	res, err := t.instance.ExpressionEngine().Evaluate(
		ctx, sl.LoopCondition(), newExecEnv(t.instance, frame, nil))
	if err != nil {
		return false, err
	}

	b, ok := res.Get(ctx).(bool)
	if !ok {
		return false, errs.New(
			errs.M("standard loop condition evaluated to a non-boolean value"),
			errs.C(errorClass, errs.TypeCastingError),
			errs.D("node_id", node.ID()))
	}

	return b, nil
}
