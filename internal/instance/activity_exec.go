package instance

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// awaitKind classifies what an activity instance is currently waiting on.
// The distinction is load-bearing twice over (ADR-025 v.3 §2.13): only an
// awaitEvent instance holds a subscription the hub must know about, and only
// an awaitEvent instance contributes to the instance's residency. An
// instance awaiting a child scope's drain or a child instance is NOT doing
// work — treating it as if it were would pin its process instance resident
// for as long as the child waits, which for an iterated Sub-Process holding
// parked User Tasks is the week-long approval case ADR-007 v.2.1 exists for.
type awaitKind uint8

const (
	// awaitNothing — executing, or finished.
	awaitNothing awaitKind = iota
	// awaitEvent — parked on an event subscription.
	awaitEvent
	// The two remaining kinds — awaiting a child scope's drain, and
	// awaiting a child instance — arrive with the sub-process and call
	// executors that can hold them (SRD-088.A M3). A leaf instance can
	// hold neither: it opens no scope and owns no child.
)

// instanceState reports one activity instance in the iteration vocabulary
// (ADR-025 v.3 §2.9.1): the ordinal identifies it, and the rest says what it
// is doing. The same shape is what the durable record persists, what the
// token view projects and what an incident carries — one vocabulary, so the
// three surfaces cannot describe an instance differently (SRD-088.C).
type instanceState struct {
	// ordinal is 0 for a non-iterated activity, which has exactly one
	// instance, and the 0-based instance number otherwise. It is the join
	// key across the record, the projection and an incident.
	ordinal int
	await   awaitKind
	done    bool
}

// activityExec executes ONE instance of an activity and owns whatever that
// instance awaits (ADR-025 v.3 §2.13).
//
// It is deliberately NOT exec.NodeExecutor (pkg/exec), which is the
// interface a NODE implements to execute itself: that answers "how does this
// node do its work", while this answers "what is running one instance of
// this activity, and what is it waiting for". A track drives one of these; a
// decorator holds N and implements the same interface, which closes the
// composition and is why a track cannot tell how many instances are behind
// the activity it is executing.
type activityExec interface {
	// run executes this instance and returns the flows to follow, or none
	// when the instance parks or belongs to a set whose decorator follows
	// the activity's flows once on its behalf.
	run(ctx context.Context) ([]*flow.SequenceFlow, error)

	// awaits reports what this instance is waiting on right now.
	awaits() awaitKind

	// state reports the instance in the iteration vocabulary.
	state() instanceState
}

// nodeExec is the leaf realization: it runs an activity that is not a scope
// host, in its own execution frame.
//
// In this slice a leaf activity has exactly one instance per track, so the
// frame identity the track already supplies is unambiguous. When a decorator
// holds several instances of one node (SRD-088.A M2), the ordinal joins that
// identity — which is why it is carried here from the start rather than
// introduced later at every call site.
type nodeExec struct {
	t    *track
	step *stepInfo
	ord  int
}

// execFor builds the executor that runs this node: a decorator when the node
// carries loop characteristics this slice has converted, a single instance
// otherwise. The caller does not care which — both satisfy activityExec, so
// a track drives one executor and cannot tell how many instances are behind
// it (ADR-025 v.3 §2.13).
//
// The remaining iteration kinds are still routed by executeStep and move here
// as they convert (SRD-088.A M2/M3).
func execFor(t *track, step *stepInfo) activityExec {
	if mi := multiInstanceOf(step.node); mi != nil && mi.IsSequential() {
		if _, composite := step.node.(scopeHost); !composite {
			return newLeafDecorator(t, step, mi)
		}
	}

	// A spawned parallel-leaf instance is ordinal 0 of nothing until it
	// says otherwise: the track it runs on carries the pass ordinal the
	// fan-out bound (setLoopCounter), and that IS this instance's
	// identity. Reading it here keeps the ordinal meaningful before
	// anything consumes it, rather than leaving a plausible zero for the
	// milestone that starts keying records on it.
	return newNodeExec(t, step, t.loopCounterSnap())
}

// leafDecorator drives the instances of an iterated LEAF activity, holding
// one executor per instance (ADR-025 v.3 §2.13). It implements activityExec
// itself, which is what closes the composition: to the track it is the thing
// that runs the activity, exactly as a single instance would be.
//
// This slice drives the SEQUENTIAL kind, where instances run one at a time
// and the decorator holds the live one. The record is unchanged by that: a
// sequential leaf's position was never a per-instance track — it is the
// iteration mirror on the host's own record — so nothing about persistence
// moves until parallel instances stop being tracks (M2b).
type leafDecorator struct {
	t    *track
	step *stepInfo
	mi   multiInstance

	// live is the instance currently executing, or nil between passes and
	// after the last one. A sequential decorator holds at most one.
	live *nodeExec
}

// newLeafDecorator builds the decorator for an iterated leaf activity.
func newLeafDecorator(
	t *track, step *stepInfo, mi multiInstance,
) *leafDecorator {
	return &leafDecorator{t: t, step: step, mi: mi}
}

// run drives every instance and follows the activity's outgoing flow ONCE,
// on exit — the activity is one token's step regardless of how many times it
// executed.
func (d *leafDecorator) run(ctx context.Context) ([]*flow.SequenceFlow, error) {
	it := miIterator{mi: d.mi}

	n, start, err := d.t.prepareSequential(ctx, it, d.mi, d.step)
	if err != nil {
		return nil, err
	}

	// N <= 0 runs zero instances — the activity itself does not execute,
	// and the token leaves via the declared outgoing flow.
	if n <= 0 {
		d.t.miState = nil

		return d.step.node.Outgoing(), nil
	}

	var nextFlows []*flow.SequenceFlow

	for i := start; i < n; i++ {
		flows, stop, err := d.runInstance(ctx, it, i, n)
		if err != nil {
			return nil, err
		}

		nextFlows = flows

		if stop {
			break
		}
	}

	if err := it.publishOutput(d.t); err != nil {
		return nil, err
	}

	d.t.miState = nil
	d.t.setLoopCounter(0)
	d.live = nil

	// a condition-stopped (or zero-flow) run still leaves via the
	// activity's declared outgoing flow, exactly once.
	if nextFlows == nil {
		nextFlows = d.step.node.Outgoing()
	}

	return nextFlows, nil
}

// runInstance executes instance i through its own executor, and refuses if
// that instance PARKS.
//
// A sequential decorator drives its instances one after another, which is
// only sound while an instance runs to completion: an instance that parked
// would return no flows, the loop would advance to the next ordinal over the
// same step, and the activity would publish its output as though every
// instance had finished. It cannot happen today — an activity that both
// iterates and parks is refused when the process is built — and this guard
// exists for the milestone that lifts that refusal, so the day the construct
// becomes reachable it fails loudly here rather than iterating past a
// waiting instance in silence.
//
// Its DECISION is tested (refuseIfParked); what stays untested is reaching
// it, which requires building the construct the snapshot refuses. SRD-088.B
// covers that end to end, in the slice that makes the construct buildable.
func (d *leafDecorator) runInstance(
	ctx context.Context, it miIterator, i, n int,
) ([]*flow.SequenceFlow, bool, error) {
	d.live = newNodeExec(d.t, d.step, i)

	flows, stop, err := d.runPass(ctx, it, i, n)
	if err != nil {
		return nil, false, err
	}

	if perr := d.refuseIfParked(i); perr != nil {
		return nil, false, perr
	}

	return flows, stop, nil
}

// refuseIfParked is the guard's decision, separated from its call site so it
// can be tested: reaching it from runInstance requires building the construct
// the snapshot refuses, but WHAT it decides — a waiting instance stops the
// iteration, naming which one — is ordinary logic and is pinned as such.
func (d *leafDecorator) refuseIfParked(i int) error {
	if d.live == nil || d.live.awaits() == awaitNothing {
		return nil
	}

	return errs.New(
		errs.M("instance %d of %q parked mid-iteration: a sequential "+
			"decorator cannot advance past a waiting instance",
			i, d.step.node.Name()),
		errs.C(errorClass, errs.InvalidState),
		errs.D(observability.AttrNodeID, d.step.node.ID()))
}

// awaits reports what the decorator's live instance awaits — the conjunction
// is trivial while at most one instance runs (ADR-025 v.3 §2.13's
// releasability rule takes its general form when parallel instances arrive).
func (d *leafDecorator) awaits() awaitKind {
	if d.live == nil {
		return awaitNothing
	}

	return d.live.awaits()
}

// state reports the ACTIVITY's iteration state: the live instance's ordinal
// and what it is doing. Its own ordinal is 0 — the activity is one instance
// of itself from the track's point of view.
func (d *leafDecorator) state() instanceState {
	if d.live == nil {
		return instanceState{ordinal: 0, await: awaitNothing}
	}

	return d.live.state()
}

// newNodeExec builds the executor for one instance of a leaf activity.
func newNodeExec(t *track, step *stepInfo, ordinal int) *nodeExec {
	return &nodeExec{t: t, step: step, ord: ordinal}
}

// run executes the node once.
func (e *nodeExec) run(ctx context.Context) ([]*flow.SequenceFlow, error) {
	return e.t.executeNode(ctx, e.step)
}

// awaits reports an event wait, which is the only kind a leaf can hold: a
// leaf opens no scope and owns no child instance.
//
// It reads the driving track's state because in this slice the two are one
// to one. A decorator's instances report their own (M2), and the reading
// here does not change: a parked leaf awaits an event.
func (e *nodeExec) awaits() awaitKind {
	if e.t.inState(TrackWaitForEvent) {
		return awaitEvent
	}

	return awaitNothing
}

// state reports this instance in the iteration vocabulary.
func (e *nodeExec) state() instanceState {
	a := e.awaits()

	return instanceState{
		ordinal: e.ord,
		await:   a,
		done:    a == awaitNothing && e.step.state == StepEnded,
	}
}

// runPass executes ONE instance of a sequential leaf activity through the
// executor the decorator holds for it. It lives on the decorator because the
// decorator OWNS the iteration (ADR-025 v.3 §2.13) — leaving it on the track
// left the driver split across both types, so the decorator delegated back to
// the thing it was replacing. Originally SRD-086 FR-1's pass: bind,
// execute in a fresh frame, capture the output, post the pass to the
// loop's iteration mirror (the checkpoint's position — a leaf opens no
// scope, so the record rides this roundtrip instead of the drain),
// evaluate the completionCondition with pass-start counts, and throw
// the behavior event. stop reports a fired condition, posted to the
// mirror the same way the composite's is (SRD-082 FR-2).
func (d *leafDecorator) runPass(
	ctx context.Context, it miIterator, i, n int,
) ([]*flow.SequenceFlow, bool, error) {
	t, step, mi := d.t, d.step, d.mi

	t.setLoopCounter(i)

	// bind loopCounter, the §2.9 counts and inputItem=collection[i] at
	// the host scope — the activity's frame resolves them by walk-up,
	// exactly as a seeded composite body does.
	if err := it.bindInstance(ctx, t, i); err != nil {
		return nil, false, err
	}

	// re-arm the step for another execution (the runStandardLoop
	// idiom): finalizeNodeExecution ended the previous pass.
	step.state = StepCreated

	flows, err := d.live.run(ctx)
	if err != nil {
		return nil, false, err
	}

	if err := d.captureOutput(ctx, i); err != nil {
		return nil, false, err
	}

	t.miState.completed++

	if _, err := t.instance.scopeExchange(ctx, scopeRequest{
		op: scopeLeafPass, host: t, n: t.miState.completed,
	}); err != nil {
		return nil, false, err
	}

	stop := false

	if t.miState.completed < n && mi.CompletionCondition() != nil {
		met, cerr := it.evalCompletion(ctx, t, step.node)
		if cerr != nil {
			return nil, false, cerr
		}

		stop = met
	}

	// the behavior event carries the CURRENT §2.9 counts (SRD-056.B):
	// republish post-drain, then throw.
	if err := t.bindMICounters(n, t.miState.completed, 0); err != nil {
		return nil, false, err
	}

	if err := t.throwMIBehavior(
		ctx, mi, step.node, t.miState.completed); err != nil {
		return nil, false, err
	}

	if stop {
		if _, err := t.instance.scopeExchange(ctx,
			scopeRequest{op: scopeNote, host: t}); err != nil {
			return nil, false, err
		}
	}

	return flows, stop, nil
}

// captureOutput reads the instance's declared output item from the
// host scope — where the leaf's UploadData just committed it — into
// the staging slot for ordinal i (SRD-086 FR-1); a no-op when the
// activity assembles no output.
func (d *leafDecorator) captureOutput(ctx context.Context, i int) error {
	t := d.t
	st := t.miState
	if st == nil || st.staging == nil {
		return nil
	}

	out, err := t.instance.sc.plane.GetData(t.scopePath, st.outputItem)
	if err != nil {
		return err
	}

	return st.staging.SetAt(ctx, i, out.Value().Get(ctx))
}
