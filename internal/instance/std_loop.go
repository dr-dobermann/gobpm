package instance

import (
	"context"
	"github.com/dr-dobermann/gobpm/pkg/observability"

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
// the node runs once (no marker, or a Multi-Instance marker). It says nothing
// about HOW the loop runs: a leaf is the only node a Standard Loop drives in
// place (ADR-025 §2.2), while a composite's passes are child scopes a
// loopDecorator opens one at a time.
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

// executeStep runs the current node through the executor that holds its
// instances (ADR-025 §2.13): a decorator when the node iterates, a single
// executor otherwise. It returns the outgoing flows to follow exactly once,
// on exit.
//
// ONE kind is still driven from here rather than by an executor: a LEAF
// Standard Loop re-runs its node in place and has no per-instance object
// yet. It converts with SRD-090.B, which is what makes an iterated waiting
// activity buildable at all.
func (t *track) executeStep(
	ctx context.Context, step *stepInfo,
) ([]*flow.SequenceFlow, error) {
	_, composite := step.node.(scopeHost)

	if sl := standardLoopOf(step.node); sl != nil && !composite {
		return t.runStandardLoop(ctx, step, sl)
	}

	return execFor(t, step).run(ctx)
}

// loopDecorator drives a COMPOSITE activity's Standard Loop, holding the
// executor for the pass currently running (ADR-025 §2.13, BPMN §13.3.6).
// It is the condition-driven sibling of iterDecorator: where a Multi-Instance
// resolves its instance count once and gives each instance its own slice of
// the input, a Standard Loop runs passes until its loopCondition says stop
// and gives them nothing but the ordinal.
//
// That difference is the whole reason it is a second type rather than a flag
// on the first. The two share no state — no cardinality, no collection, no
// staging, no completionCondition — and the only thing they have in common is
// what they hold, which is exactly the interface (ADR-025 §2.13).
type loopDecorator struct {
	t    *track
	step *stepInfo
	sl   standardLoop

	// live is the pass currently executing, or nil between passes and once
	// the loop has finished. A Standard Loop runs one pass at a time.
	live activityExec
}

// newLoopDecorator builds the decorator for a looped composite activity.
func newLoopDecorator(
	t *track, step *stepInfo, sl standardLoop,
) *loopDecorator {
	return &loopDecorator{t: t, step: step, sl: sl}
}

// run drives the passes and follows the composite's outgoing flow once, on
// exit — the activity is one token's step however many times its body ran.
//
// Each pass publishes its 0-based ordinal (track field + host-scope datum) so
// the condition and the body resolve it by name via walk-up, and so it
// survives the child close for the next pass's test (§4.6). The bind is a
// plane write, mutex-safe, made here rather than by the loop so it is set
// before the continuation test reads it and before the scope opens.
func (d *loopDecorator) run(ctx context.Context) ([]*flow.SequenceFlow, error) {
	t, step := d.t, d.step

	// a Standard Loop resumes from the completed count alone (miSeed below)
	// — its passes are strictly ordered, so there is no set of out-of-order
	// ordinals to read. The executor set is still TAKEN, because leaving it
	// on the track would hand this activity's restored position to the next
	// iterated activity the token reaches (SRD-090.A FR-7).
	_ = t.takeIterSeed()

	// a restored host resumes at its recorded pass (SRD-082 FR-3):
	// completed passes are never re-run; the loop condition re-evaluates
	// naturally at the seeded pass over the restored scope data.
	first := 0
	if t.miSeed != nil {
		first = t.miSeed.Completed
		t.miSeed = nil
	}

	for pass := first; ; pass++ {
		t.setLoopCounter(pass)

		if err := t.instance.sc.bindLoopCounterAt(t.scopePath, pass); err != nil {
			return nil, err
		}

		// pre-tested (while) tests every pass; post-tested (do-while) skips
		// the first — one test site, matching runStandardLoop.
		if d.sl.TestBefore() || pass > 0 {
			cont, err := t.evalLoopCond(ctx, step.node, d.sl)
			if err != nil {
				return nil, err
			}

			if !cont {
				break
			}
		}

		if err := d.runPass(ctx, pass); err != nil {
			return nil, err
		}

		if m, ok := d.sl.LoopMaximum(); ok && pass+1 >= m {
			break
		}
	}

	d.live = nil

	// the loop finished — follow the composite's outgoing once (SubProcess
	// selects it; the body already ran through the scopes, so this only
	// routes the token onward), mirroring runStandardLoop's single
	// post-loop exit.
	return t.executeNode(ctx, step)
}

// runPass runs one pass as its own instance: the executor opens that pass's
// child scope and parks for its drain.
func (d *loopDecorator) runPass(ctx context.Context, pass int) error {
	d.live = newScopeExec(d.t, d.step, pass)

	_, err := d.live.run(ctx)

	return err
}

// awaits reports what the pass currently running awaits — a child scope's
// drain, or nothing between passes. The conjunction ADR-025 §2.13 states
// is trivial here: a Standard Loop holds one pass at a time.
func (d *loopDecorator) awaits() awaitKind {
	if d.live == nil {
		return awaitNothing
	}

	return d.live.awaits()
}

// state reports the ACTIVITY's iteration state: the live pass's ordinal and
// what it is doing. Its own ordinal is 0 — the activity is one instance of
// itself from the track's point of view.
func (d *loopDecorator) state() instanceState {
	if d.live == nil {
		return instanceState{ordinal: 0, await: awaitNothing}
	}

	return d.live.state()
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
			errs.D(observability.AttrNodeID, node.ID()))
	}

	return b, nil
}
