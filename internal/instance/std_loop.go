package instance

import (
	"context"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"sync/atomic"

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
// ONE decision, with no exceptions left (SRD-090.A FR-2/FR-11). The leaf
// Standard Loop was the last one driven from here — it re-ran its node in
// place, with no per-instance object — and it is a loopDecorator over node
// executors now, which is the same shape every other iterated activity
// already had.
func (t *track) executeStep(
	ctx context.Context, step *stepInfo,
) ([]*flow.SequenceFlow, error) {
	// Arrival already resolved this node to its executor and stored it
	// (SRD-090.B FR-1/FR-2), so REUSE it: building a second one here would
	// give the activity two objects with one identity, and the subscription
	// set would live on the object that is not dispatching. Resolve here
	// only for a node arrival never classified — every non-waiting node.
	e := t.resolveExec(step)

	// the loop asks the executor what this activity awaits while it runs
	// (SRD-090.A FR-8) — a composite parked for its body's drain is NOT
	// doing work, and only the executor can say so. Cleared on the way out,
	// including the error path: a track between steps awaits nothing.
	defer t.exec.Store(nil)

	return e.run(ctx)
}

// awaits reports what this track's activity is waiting on right now, asked
// of the executor that is running it (SRD-090.A FR-8, ADR-025 §2.13).
//
// It is not a question about the NODE — the loop never learns whether the
// activity iterates, how many instances it holds, or what kind they are
// (FR-11). It learns one thing: whether this token's goroutine is doing work
// or holding something open on someone else's behalf.
func (t *track) awaits() awaitKind {
	h := t.exec.Load()
	if h == nil {
		return awaitNothing
	}

	return h.e.awaits()
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
// composite records which realization this activity's passes take — a child
// scope, or an execution of the node — the same one difference iterDecorator
// carries, leaking in the same three places and named where each occurs.
type loopDecorator struct {
	t    *track
	step *stepInfo
	sl   standardLoop

	// live is the pass currently executing, or nil between passes and once
	// the loop has finished. A Standard Loop runs one pass at a time.
	//
	// Atomic because the decorator's own goroutine writes it while the LOOP
	// reads it to ask what this activity awaits (SRD-090.A FR-8). The handle
	// on the track is atomic too, but that only fences WHICH executor is
	// current — this fences the field it points at.
	live atomic.Pointer[execHandle]

	// lastFlows is the flows the most recent LEAF pass produced. A leaf's
	// node execution is the activity, so the flows it selected on its final
	// pass are the activity's; a composite re-runs its node once on exit
	// instead (exitFlows).
	lastFlows []*flow.SequenceFlow

	// eventSubs makes the decorator the hub's subscriber for this activity's
	// waits — see iterDecorator (ADR-006 §2.9.5, SRD-090.B FR-1). A Standard
	// Loop holds one pass at a time, so its waiting set never exceeds one.
	//
	// Embedded LAST: it ends in non-pointer fields, so the pointers declared
	// after it would not be contiguous (govet fieldalignment).
	eventSubs

	composite bool
}

// newLoopDecorator builds the decorator for a Standard-Loop activity, leaf
// or composite.
func newLoopDecorator(
	t *track, step *stepInfo, sl standardLoop, composite bool,
) *loopDecorator {
	return &loopDecorator{
		eventSubs: subsIDFor(t, step.node),
		t:         t,
		step:      step,
		sl:        sl,
		composite: composite,
	}
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

	// one step of the token, however many passes run (ADR-025 §2.13b.1e).
	t.updateState(TrackExecutingStep)
	t.record(TrackExecutingStep)
	t.updateState(TrackIterating)

	for pass := first; ; pass++ {
		// the track-side counter is the COMPOSITE's position, read by the
		// scope facts and the capture. A leaf loop has no scope and has
		// never recorded one, so setting it here would change what a
		// checkpoint says about a leaf's pass.
		if d.composite {
			t.setLoopCounter(pass)
		}

		if err := t.instance.sc.bindLoopCounterAt(t.scopePath, pass); err != nil {
			return nil, err
		}

		// the engine's own names for this pass, from the one builder every
		// publication path shares (iterationvars.go), at the same scope the
		// counter is bound to.
		for _, b := range iterationBindings(
			t.scopePath.String(), iterKindStdLoop, d.step.node, pass) {
			if err := t.instance.sc.bindDataItemAt(
				t.scopePath, b.name, b.value); err != nil {
				return nil, err
			}
		}

		// pre-tested (while) tests every pass; post-tested (do-while) skips
		// the first — one test site for both.
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

	d.live.Store(nil)

	// the exit runs while STILL iterating — a composite's exit executes its
	// node, and leaving the state first would record a second step for an
	// activity that is one step of its token.
	flows, err := d.exitFlows(ctx)
	if err != nil {
		return nil, err
	}

	t.updateState(TrackProcessStepResults)

	return flows, nil
}

// iterKind names the shape this decorator drives, for the loop's position
// mirror (SRD-090.A FR-6). A Standard Loop has exactly one — the decorator
// states it rather than the loop deriving it from the node (FR-11).
func (d *loopDecorator) iterKind() string {
	return iterKindStdLoop
}

// subscriber: see iterDecorator.subscriber — one subscription for the
// activity, held across its passes (SRD-090.B FR-1/FR-2).
// deliverTo hands the occurrence to the pass that is parked on it. A Standard
// Loop runs ONE pass at a time, so the track's channel is that pass's — see
// iterDecorator.deliverTo.
func (d *loopDecorator) deliverTo(_ int, eDef flow.EventDefinition) bool {
	return d.t.offerToPass(eDef)
}

func (d *loopDecorator) subscriber() activitySubscriber { return d }

// runPass runs one pass as its own instance: the executor opens that pass's
// child scope and parks for its drain.
func (d *loopDecorator) runPass(ctx context.Context, pass int) error {
	// the field says "nil between passes" and now is: the loop asks what
	// this activity awaits at any moment, and a finished pass is not what
	// it is awaiting. Harmless today — a finished executor already reports
	// awaitNothing — but the doc and the code disagreeing is how the next
	// reader gets it wrong.
	defer d.live.Store(nil)

	if d.composite {
		e := newScopeExec(d.t, d.step, pass)
		e.iterKind = d.iterKind()
		d.live.Store(&execHandle{e: e, node: e.step.node})

		_, err := e.run(ctx)

		return err
	}

	// a LEAF re-runs its node, so the step must be re-armed: the previous
	// pass ended it at StepEnded (finalizeNodeExecution). A composite's node
	// is not executed per pass at all — its body runs in the child scope —
	// so it has nothing to re-arm.
	d.step.state = StepCreated

	e := newNodeExec(d.t, d.step, pass)
	d.live.Store(&execHandle{e: e, node: e.step.node})

	flows, err := e.run(ctx)
	if err != nil {
		return err
	}

	d.lastFlows = flows

	return nil
}

// exitFlows follows the activity's outgoing flow ONCE, on exit.
//
// A composite re-runs its node: the body already ran in the child scopes and
// the node execution is what selects the flow. A leaf must NOT — executing
// the node IS the activity, and the passes already did that, so the flows
// its LAST pass selected are the activity's.
//
// A pre-tested loop whose condition is false at entry runs zero passes and
// selected nothing, but the token still leaves by the activity's outgoing
// flow (BPMN §13.3.6). A leaf loop carries no conditional flow selection, so
// its declared outgoing is the answer.
func (d *loopDecorator) exitFlows(
	ctx context.Context,
) ([]*flow.SequenceFlow, error) {
	if d.composite {
		return d.t.executeNode(ctx, d.step)
	}

	if d.lastFlows == nil {
		return d.step.node.Outgoing(), nil
	}

	return d.lastFlows, nil
}

// awaits reports what the pass currently running awaits — a child scope's
// drain, or nothing between passes. The conjunction ADR-025 §2.13 states
// is trivial here: a Standard Loop holds one pass at a time.
func (d *loopDecorator) awaits() awaitKind {
	h := d.live.Load()
	if h == nil {
		return awaitNothing
	}

	return h.e.awaits()
}

// state reports the ACTIVITY's iteration state: the live pass's ordinal and
// what it is doing. Its own ordinal is 0 — the activity is one instance of
// itself from the track's point of view.
func (d *loopDecorator) state() instanceState {
	h := d.live.Load()
	if h == nil {
		return instanceState{ordinal: 0, await: awaitNothing}
	}

	return h.e.state()
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
