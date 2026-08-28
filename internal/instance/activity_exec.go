package instance

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// awaitKind classifies what an activity instance is currently waiting on.
// The distinction is load-bearing twice over (ADR-025 §2.13): only an
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
	// awaitScope — parked for the drain of a child scope this instance
	// opened (scopeExec). The body's tokens are doing the work.
	awaitScope
	// The last kind — awaiting a child INSTANCE — arrives with the call
	// executor that can hold it (SRD-090.A M3d). A leaf instance can hold
	// none of the two: it opens no scope and owns no child.
)

// instanceState reports one activity instance in the iteration vocabulary
// (ADR-025 §2.9.1): the ordinal identifies it, and the rest says what it
// is doing. The same shape is what the durable record persists, what the
// token view projects and what an incident carries — one vocabulary, so the
// three surfaces cannot describe an instance differently (SRD-090.C).
type instanceState struct {
	// ordinal is 0 for a non-iterated activity, which has exactly one
	// instance, and the 0-based instance number otherwise. It is the join
	// key across the record, the projection and an incident.
	ordinal int
	await   awaitKind
	done    bool
}

// activityExec executes ONE instance of an activity and owns whatever that
// instance awaits (ADR-025 §2.13).
//
// It is deliberately NOT exec.NodeExecutor (pkg/exec), which is the
// interface a NODE implements to execute itself: that answers "how does this
// node do its work", while this answers "what is running one instance of
// this activity, and what is it waiting for". A track drives one of these; a
// decorator holds N and implements the same interface, which closes the
// composition and is why a track cannot tell how many instances are behind
// the activity it is executing.
// **Two goroutines call this interface, and an implementor has to know
// which.** `run` executes on the TOKEN's goroutine and is the only method
// that may mutate; `awaits` and `state` are called by the single-writer LOOP
// while run is still in flight — that is the whole point of them — so they
// must be safe to call concurrently with it and must not mutate. Anything
// one writes and the others read is fenced by the implementor (an atomic
// flag, an atomic pointer), never by luck of the call graph.
//
// `run`'s error is also a channel: a returned `errDehydrated` means the loop
// released this executor mid-flight and the run loop must unwind WITHOUT
// treating it as a discard or a failure. Any implementor or decorator that
// wraps errors opaquely breaks that — return it as it came.
type activityExec interface {
	// run executes this instance and returns the flows to follow, or none
	// when the instance parks or belongs to a set whose decorator follows
	// the activity's flows once on its behalf. TOKEN goroutine.
	run(ctx context.Context) ([]*flow.SequenceFlow, error)

	// awaits reports what this instance is waiting on right now. Called
	// from the LOOP goroutine, concurrently with run.
	awaits() awaitKind

	// state reports the instance in the iteration vocabulary. Called from
	// the LOOP goroutine, concurrently with run.
	state() instanceState

	// subscriber reports who registers with the hub for this activity's
	// waits, or nil to leave the existing per-trigger rule alone
	// (SRD-090.B FR-1).
	//
	// The question is asked of the executor rather than answered by a test
	// on the node, which is the whole shape of this slice: a driver never
	// learns that an activity iterates, it learns who owns the wait. A leaf
	// or a composite instance answers nil — it has one execution, which is
	// its own subscriber (ADR-006 §2.9.5 Scope) — and a decorator answers
	// itself, holding one subscription for the activity across every pass.
	subscriber() activitySubscriber
}

// nodeExec is the leaf realization: it runs an activity that is not a scope
// host, in its own execution frame.
//
// In this slice a leaf activity has exactly one instance per track, so the
// frame identity the track already supplies is unambiguous. When a decorator
// holds several instances of one node (SRD-090.A M2), the ordinal joins that
// identity — which is why it is carried here from the start rather than
// introduced later at every call site.
type nodeExec struct {
	t    *track
	step *stepInfo

	// capture takes this instance's declared output while it is still the
	// instance's own — see activityInstance.capture.
	capture func(f *scope.Frame) error

	// received is THIS delivery's payload, taken here rather than on the
	// track for the same reason `local` is: the track's slot is one field
	// shared by N instances delivered to on N goroutines (see track.deliver).
	//
	// Written and read by this instance's own goroutine — the delivery it
	// woke on and the binding that follows — so it needs no lock.
	received *data.ItemDefinition

	// local is this instance's own data, bound frame-local so a concurrent
	// sibling cannot overwrite it (SRD-090.A FR-4). nil for the single
	// instance of a plain activity, which has nothing to distinguish.
	local []data.Data

	ord int

	// parked is what THIS execution awaits, owned here rather than read off
	// the track (ADR-025 §2.13: "a node executor … owns that node's wait";
	// §2.13b.1e: "a track's state is what its executor awaits").
	//
	// Reading the track's state instead works only while a track and an
	// execution are one to one, which a parallel fan-out breaks: N instances
	// share one track, so one instance parking would read as all of them
	// parking, and the barrier would proceed on the first. Owned per
	// execution, the decorator's await becomes the conjunction over its
	// instances, which is what makes it wait for every approval.
	//
	// Written by the executing goroutine before it blocks and read by the
	// decorator's, so it is atomic. A bool rather than an awaitKind because
	// an event wait is the only kind a leaf can hold — it opens no scope and
	// owns no child instance.
	parked atomic.Bool

	// finished marks an instance whose run has returned, however it ended.
	//
	// It is what separates the two ways of not being parked. An instance
	// EXECUTING its node is doing work and its track must stay resident; an
	// instance that has FINISHED is doing nothing, and treating the two alike
	// would pin the process instance for as long as its remaining approvals
	// take — the residency bug ADR-007 v.2.1 exists to prevent, reintroduced
	// one level down.
	finished atomic.Bool

	// concurrent marks an instance of a PARALLEL fan-out — one of N running
	// at the same time on one track.
	//
	// It decides whether this execution may fall back to reading the track's
	// wait state. A SEQUENTIAL pass may: one instance runs at a time, so the
	// track's state is that instance's however high its ordinal. A concurrent
	// one may not: its siblings share the track, so the answer would be
	// theirs as much as its own.
	concurrent bool
}

// execFor builds the executor that runs this node: a decorator when the node
// carries loop characteristics this slice has converted, a single instance
// otherwise. The caller does not care which — both satisfy activityExec, so
// a track drives one executor and cannot tell how many instances are behind
// it (ADR-025 §2.13).
//
// Every kind routes through here now (SRD-090.A M3c): the leaf Standard
// Loop was the last exception, and it is a decorator over node executors
// like the rest.
func execFor(t *track, step *stepInfo) activityExec {
	_, composite := step.node.(scopeHost)

	if sl := standardLoopOf(step.node); sl != nil {
		return newLoopDecorator(t, step, sl, composite)
	}

	if mi := multiInstanceOf(step.node); mi != nil {
		return newIterDecorator(t, step, mi, composite)
	}

	// A plain activity is instance zero of one — the common case kept
	// uniform rather than special-cased, INCLUDING a plain composite
	// (SRD-090.A M3c). It used to decide earlier and elsewhere: it parked
	// on entry for a loop-driven scope re-entry, so its single instance
	// was never executed from a step at all, and FR-2's "one decision"
	// was two.
	//
	// Routing it here is also what makes FR-8 one rule: a plain composite
	// host is not a wait, its token forked into a child scope, and asking
	// the NODE whether its wait can be externalized was a category error
	// that pinned every Sub-Process in memory.
	if composite {
		return newPlainScopeExec(t, step)
	}

	return newNodeExec(t, step, 0)
}

// iterDecorator drives the instances of a Multi-Instance activity, holding
// one executor per instance (ADR-025 §2.13). It implements activityExec
// itself, which is what closes the composition: to the track it is the thing
// that runs the activity, exactly as a single instance would be.
//
// It is indifferent to WHAT an instance is. A leaf activity's instance is an
// execution of its node; a composite activity's instance is a child scope
// (scopeExec). Everything the iteration itself decides — how many instances,
// what each one is given, when the completionCondition stops the run, what
// the assembled output is — is the same question in both cases, which is why
// one decorator answers it and the two executors differ only in what they do
// when run.
//
// composite records which realization this activity's instances take. It is
// the one difference that leaks past the executor boundary, in three places
// that are named where they occur: the step is re-armed per pass only for a
// leaf, the declared output is taken by the decorator only for a leaf (the
// loop takes a composite's before its scope closes, the last moment it
// exists), and the exit follows the flows differently.
type iterDecorator struct {
	t    *track
	step *stepInfo
	mi   multiInstance

	// live is the instance currently executing, or nil between passes and
	// after the last one. A sequential decorator holds at most one.
	//
	// Atomic because the decorator's own goroutine writes it while the LOOP
	// reads it to ask what this activity awaits (SRD-090.A FR-8). The handle
	// on the track is atomic too, but that only fences WHICH executor is
	// current — this fences the field it points at.
	live atomic.Pointer[execHandle]

	// fanned is the instance set of a PARALLEL run, held while the barrier
	// awaits them. `live` names the ONE instance a sequential pass is
	// running, which cannot describe N at once — and what the activity
	// awaits is the conjunction over all of them (ADR-025 §2.13b.1e).
	//
	// Guarded by its own mutex: the decorator's goroutine builds and clears
	// it while the loop reads it to ask what this activity awaits.
	fanned []*nodeExec

	// seed is the restored executor set this activity resumes from, taken
	// from the track at the start of the run (SRD-090.A FR-7).
	seed *checkpoint.IterationRecord

	// eventSubs makes the decorator the hub's subscriber for this activity's
	// waits (ADR-006 §2.9.5, SRD-090.B FR-1): one identity per iterated
	// activity, one subscription per definition, alive while any instance
	// awaits it.
	eventSubs

	// fannedMu guards fanned. Placed here rather than beside it so the
	// struct's pointer-bearing fields stay grouped (fieldalignment).
	fannedMu sync.Mutex

	composite bool
}

// newIterDecorator builds the decorator for an iterated activity.
func newIterDecorator(
	t *track, step *stepInfo, mi multiInstance, composite bool,
) *iterDecorator {
	return &iterDecorator{
		eventSubs: subsIDFor(t, step.node),
		t:         t,
		step:      step,
		mi:        mi,
		composite: composite,
	}
}

// buildInstance makes the executor for ordinal ord: a child scope for a
// composite activity, an execution of the node for a leaf.
func (d *iterDecorator) buildInstance(ord int) activityExec {
	if d.composite {
		e := newScopeExec(d.t, d.step, ord)
		e.iterKind = d.iterKind()

		return e
	}

	return newNodeExec(d.t, d.step, ord)
}

// iterKind names the shape this decorator drives, for the loop's position
// mirror (FR-6). The decorator states it rather than the loop deriving it
// from the node — see scopeRequest.iterKind.
func (d *iterDecorator) iterKind() string {
	if d.mi != nil && d.mi.IsSequential() {
		return iterKindMISequential
	}

	return iterKindMIParallel
}

// recordIteration reports this activity's account to the instance's durable
// register (ADR-025 §2.9.2). The counts at the activity's own scope end with
// the activation; this is what answers "how many did we process?" from a
// later node, and what §2.6.1's map key has to key on.
//
// Keyed by the activity's id, so two iterated activities running at once —
// a parallel gateway with a Multi-Instance on each arm — stay distinguishable,
// which a flat runtime name could not manage.
func (d *iterDecorator) recordIteration(total, completed, terminated int) {
	if d.step == nil || d.step.node == nil || d.t == nil {
		return
	}

	d.t.instance.iterations.record(d.step.node.ID(), iterationFact{
		Kind:       d.iterKind(),
		Total:      total,
		Completed:  completed,
		Terminated: terminated,
	})
}

// exitFlows follows the activity's outgoing flow ONCE, on exit.
//
// A composite re-runs executeNode: its node execution is what selects the
// flow, and the body already ran in the child scopes. A leaf must NOT —
// executing the node IS the activity, and the instances already did that N
// times (SRD-086 FR-3), so its declared outgoing flows are followed
// directly.
func (d *iterDecorator) exitFlows(
	ctx context.Context,
) ([]*flow.SequenceFlow, error) {
	if d.composite {
		return d.t.executeNode(ctx, d.step)
	}

	return d.step.node.Outgoing(), nil
}

// run drives every instance and follows the activity's outgoing flow ONCE,
// on exit — the activity is one token's step regardless of how many times it
// executed.
func (d *iterDecorator) run(ctx context.Context) ([]*flow.SequenceFlow, error) {
	it := miIterator{mi: d.mi}

	d.seed = d.t.takeIterSeed()

	n, start, err := d.t.prepareIteration(ctx, it, d.mi, d.step)
	if err != nil {
		return nil, err
	}

	// N <= 0 runs zero instances — the activity itself does not execute,
	// and the token leaves via the activity's outgoing flow.
	if n <= 0 {
		d.t.miState = nil

		return d.exitFlows(ctx)
	}

	if !d.mi.IsSequential() {
		return d.runParallel(ctx, it, n)
	}

	// the activity is ONE step of its token, so it transitions and records
	// once here; TrackIterating then puts the passes below the state
	// machine's granularity (ADR-025 §2.13b.1e). Until M3f the sequential
	// path did the opposite of the parallel one — every pass transitioned
	// and recorded, so one activity reported N step executions.
	d.t.updateState(TrackExecutingStep)
	d.t.record(TrackExecutingStep)
	d.t.updateState(TrackIterating)

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
	d.live.Store(nil)

	// a condition-stopped (or zero-flow) run still leaves via the
	// activity's outgoing flow, exactly once. A composite's instances
	// return no flows at all — its node has not executed yet — so the exit
	// is always the one that produces them.
	//
	// The exit runs while the track is STILL iterating, and leaving the
	// state early is a real error rather than a tidiness question: a
	// composite's exit executes its node, so it would record a SECOND step
	// for an activity that is one step of its token.
	exit := nextFlows
	if nextFlows == nil || d.composite {
		flows, ferr := d.exitFlows(ctx)
		if ferr != nil {
			return nil, ferr
		}

		exit = flows
	}

	d.t.updateState(TrackProcessStepResults)

	return exit, nil
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
// it, which requires building the construct the snapshot refuses. SRD-090.B
// covers that end to end, in the slice that makes the construct buildable.
func (d *iterDecorator) runInstance(
	ctx context.Context, it miIterator, i, n int,
) ([]*flow.SequenceFlow, bool, error) {
	d.live.Store(&execHandle{e: d.buildInstance(i)})

	// cleared when this instance is done, so the field means what it says:
	// nil between instances (see loopDecorator.runPass).
	defer d.live.Store(nil)

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
func (d *iterDecorator) refuseIfParked(i int) error {
	h := d.live.Load()
	if h == nil || h.e.awaits() == awaitNothing {
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
// is trivial while at most one instance runs (ADR-025 §2.13's
// releasability rule takes its general form when parallel instances arrive).
// busyInstances reports whether any instance of a fanned-out activity is
// executing rather than parked (ADR-025 §2.13b.1e, from the residency side).
func (d *iterDecorator) busyInstances() bool {
	// THE DECORATOR ANSWERS FOR ITS INSTANCES, because it is the one
	// executing them (ADR-025 §2.15a). Busy means a delivery is queued or
	// being applied; a released track cannot be taken away underneath it.
	return d.busy()
}

func (d *iterDecorator) awaits() awaitKind {
	// a PARALLEL run: the activity awaits whatever ANY instance still
	// awaits. The conjunction is the point — with N instances sharing a
	// track, reporting only one of them would let the activity read as
	// finished while somebody still holds work (ADR-025 §2.13b.1e).
	d.fannedMu.Lock()
	fanned := d.fanned
	d.fannedMu.Unlock()

	for _, e := range fanned {
		if k := e.awaits(); k != awaitNothing {
			return k
		}
	}

	if len(fanned) > 0 {
		return awaitNothing
	}

	// a SEQUENTIAL pass: one instance at a time, so the live one IS the
	// conjunction.
	h := d.live.Load()
	if h == nil {
		return awaitNothing
	}

	return h.e.awaits()
}

// setFanned records the instance set a parallel run is awaiting, so the
// activity can answer what it awaits as the conjunction over them. Cleared
// when the barrier is done with them.
func (d *iterDecorator) setFanned(ee []*nodeExec) {
	d.fannedMu.Lock()
	defer d.fannedMu.Unlock()

	d.fanned = ee
}

// state reports the ACTIVITY's iteration state: the live instance's ordinal
// and what it is doing. Its own ordinal is 0 — the activity is one instance
// of itself from the track's point of view.
func (d *iterDecorator) state() instanceState {
	h := d.live.Load()
	if h == nil {
		return instanceState{ordinal: 0, await: awaitNothing}
	}

	return h.e.state()
}

// subscriber: the decorator holds ONE subscription per definition for the
// whole activity, across every pass (ADR-006 §2.9.5, SRD-090.B FR-1/FR-2).
// completeInstance routes a COMPLETION to the instance whose work it is.
//
// Ungated, unlike deliverTo: an event may reach an instance that is no longer
// waiting — a losing arm, a sibling that finished in flight — and dropping it
// is correct. A completion is work somebody has already performed, and the
// instance it belongs to routinely has not parked yet: a restored fan-out
// registers its tasks before its decorator runs, so the first completion
// arrives ahead of the instances. Dropping that marks the work done and waits
// forever for it to be done again.
func (d *iterDecorator) completeInstance(
	ord int, def flow.EventDefinition, owner string,
) {
	d.t.recordIterationOwner(d.step.node, ord, owner)

	if d.fansOutLeaves() {
		d.deliver(ord, def)

		return
	}

	d.t.offerToPass(def)
}

// deliverTo routes an occurrence to the instance it is for.
//
// THE DECORATOR DECIDES, because it is what executes the instances (ADR-025
// §2.15a). A parallel fan-out queues the delivery and applies it serially on
// its own goroutine — the instances are state it owns, and the node they share
// is the token's. Every other shape runs ONE pass at a time, and that pass is
// parked on the track's own channel, which is where its delivery goes.
func (d *iterDecorator) deliverTo(ord int, eDef flow.EventDefinition) bool {
	if d.fansOutLeaves() {
		return d.eventSubs.deliverTo(ord, eDef)
	}

	return d.t.offerToPass(eDef)
}

// fansOutLeaves reports whether this run holds N instances of one shared node
// at once — the only shape whose deliveries are applied serially.
func (d *iterDecorator) fansOutLeaves() bool {
	return !d.composite && d.mi != nil && !d.mi.IsSequential()
}

func (d *iterDecorator) subscriber() activitySubscriber { return d }

// runParallel drives every instance of a parallel leaf activity at once and
// awaits them all (ADR-025 §2.13, SRD-090.A FR-5). The N-of-N barrier is
// ordinary control flow on the decorator's own goroutine: no loop-owned
// group, no per-instance scope, and nothing spawns a track to iterate.
//
// The instances are built BEFORE any of them starts, so a build failure —
// a collection element that will not read — faults with nothing running.
func (d *iterDecorator) runParallel(
	ctx context.Context, it miIterator, n int,
) ([]*flow.SequenceFlow, error) {
	t, step := d.t, d.step

	// the activity is ONE step of its token however many instances run it,
	// so the transitions are made here, once. TrackIterating then puts the
	// instances below the state machine's granularity, so none of them can
	// report a step of its own (ADR-025 §2.13b.1e).
	t.updateState(TrackExecutingStep)
	t.record(TrackExecutingStep)
	t.updateState(TrackIterating)

	// a parallel instance completes out of order, so its slot is addressed
	// by ordinal — SetAt replaces rather than appends, which needs the
	// array pre-sized to N (§2.6); a canceled slot keeps its nil (§2.7).
	if st := t.miState; st.staging != nil {
		st.staging = presizedStaging(ctx, st.staging, n)
	}

	outs := newInstanceOutputs(n)

	// a RESTORED position never re-runs a completed ordinal (FR-7). The
	// count alone cannot say which those are — a parallel instance
	// completes out of order — so the recorded set is what decides, and
	// its outputs are already in the restored staging.
	states := restoredStates(d.seed, n)

	// a RESTORED instance takes back the identity it was announced under,
	// before anything asks it for one. Without this each rebuilt instance
	// would mint a fresh id and every reference a person or a UI is holding
	// would name nothing (ADR-020 §2.12, SRD-071 FR-8 at iteration
	// granularity).
	if d.seed != nil {
		for _, inst := range d.seed.Instances {
			d.adoptTaskID(inst.Ordinal, inst.TaskID)
		}
	}

	// a completion that arrived before this run existed belongs to one of the
	// instances about to be built — taken here, where the track's goroutine
	// owns both sides, and handed over when that instance parks.
	for ord, def := range d.t.takePendingCompletions() {
		d.deliver(ord, def)
	}

	insts := make(map[int]activityExec, n)

	// a COMPOSITE instance's output is read loop-side, into the cell the
	// executor carries; the barrier collects it once the instance reports.
	caps := make(map[int]*instanceCapture, n)

	for ord, st := range states {
		if st == instanceCompleted {
			continue
		}

		e, icap, err := d.instanceFor(ctx, ord, outs)
		if err != nil {
			return nil, err
		}

		insts[ord] = e

		if icap != nil {
			caps[ord] = icap
		}
	}

	// the set is posted BEFORE the first instance starts, and the roundtrip
	// is what fences it. Until then nothing has told the capture which
	// ordinals are live: a checkpoint landing in that window would record an
	// EMPTY set, which restores as "all N still to run" — so an activity
	// interrupted mid-fan-out would re-run the instances a restore had
	// already found complete (SRD-090.A FR-6/FR-7).
	if err := d.postPosition(ctx, n-len(insts), states); err != nil {
		return nil, err
	}

	runCtx, cancelRest := context.WithCancel(ctx)
	defer cancelRest()

	done := make(chan instanceDone, n)

	// the activity's await is the conjunction over these (ADR-025 §2.13b.1e),
	// so the set is posted before any of them starts: an instance that parks
	// immediately must already be counted, or the activity would read as
	// awaiting nothing while somebody holds work.
	leaves := make([]*nodeExec, 0, len(insts))

	for _, e := range insts {
		if le, ok := e.(*nodeExec); ok {
			le.concurrent = true
			leaves = append(leaves, le)
		}
	}

	d.setFanned(leaves)
	defer d.setFanned(nil)

	run := parallelRun{
		n:          n,
		done:       done,
		outs:       outs,
		caps:       caps,
		execs:      insts,
		cancelRest: cancelRest,
		states:     states,
		launched:   len(insts),
	}

	// A COMPOSITE's instances are child SCOPES with tracks of their own, so
	// they run concurrently and report — §2.15a's claim is about the decorated
	// NODE, which a composite instance does not share.
	if d.composite {
		d.launchAll(runCtx, run)

		return d.awaitParallel(ctx, it, run, step)
	}

	// THE DECORATOR PARKS EVERY INSTANCE'S WAIT ITSELF, before any of them is
	// applied, so all N are outstanding at once — which is what "parallel"
	// means for a Multi-Instance holding work.
	parked, err := d.parkInstances(insts, step)
	if err != nil {
		return nil, err
	}

	// A FAN-OUT THAT HOLDS NO WAIT genuinely runs at once — a Script or
	// Service Task instance does its work rather than waiting for somebody,
	// and §2.15a's rule is about the one that APPLIES a delivery.

	if len(parked) == 0 {
		d.launchAll(runCtx, run)

		return d.awaitParallel(ctx, it, run, step)
	}

	// A FAN-OUT THAT HOLDS N WAITS applies their completions SERIALLY, on
	// this goroutine (ADR-025 §2.15a).
	//
	// The concurrency it exists for is external — N people acting at the same
	// time, in the distributor's inbox. Inside, applying one completion is
	// small and bounded, and a goroutine per instance made the node they
	// share mutable state with no natural owner: two approvers' outputs could
	// cross, because the node buffers the completion it was handed
	// (activities.UserTask.completedOutputs).
	return d.applyParallel(ctx, it, run, step)
}

// instanceDone is one parallel instance reporting its ordinal and verdict.
type instanceDone struct {
	err error
	ord int
}

// parallelRun is the in-flight state of a parallel fan-out: the frozen
// instance count, the completion channel, the per-ordinal captured outputs
// and the handle that stops the instances still running.
type parallelRun struct {
	done chan instanceDone
	outs *instanceOutputs
	// execs is the instance set, by ordinal. The decorator applies a
	// delivery by looking its instance up here — the instances are state it
	// owns, not goroutines with mailboxes of their own (ADR-025 §2.15a).
	execs map[int]activityExec
	// caps holds a COMPOSITE instance's output cell, filled loop-side
	// before its scope closed and read here once it reports. Empty for a
	// leaf fan-out, whose instances capture through their own frames.
	caps       map[int]*instanceCapture
	cancelRest context.CancelFunc
	states     []string
	n          int
	// launched counts the instances this run actually started — N on a
	// fresh activation, fewer when a restore found some already complete.
	// The barrier awaits THAT many reports; awaiting N would hang forever
	// on a resumed activity.
	launched int
}

// stopRemaining ends the instances still running when the
// completionCondition fires (SRD-056.A §2.7): it cancels their shared
// context, then tears down the scopes that context cannot close for them.
func (d *iterDecorator) stopRemaining(
	ctx context.Context, run parallelRun,
) error {
	run.cancelRest()

	return d.cancelInstanceScopes(ctx)
}

// cancelInstanceScopes asks the loop to tear down the instance scopes of
// the instances this decorator has just stopped.
//
// The instances cannot do it themselves: each wakes from awaitDrain on the
// context stopRemaining just canceled, and a scope request honors ctx, so
// the request would fail and leak the very scope it meant to close. The
// teardown belongs to whoever canceled (SRD-090.A M3b).
//
// A no-op for a leaf, which opens no scopes, so the barrier can call it
// unconditionally.
func (d *iterDecorator) cancelInstanceScopes(ctx context.Context) error {
	if !d.composite {
		return nil
	}

	_, err := d.t.instance.scopeExchange(ctx, scopeRequest{
		op: scopeCancelInstances, host: d.t, node: d.step.node,
	})

	return err
}

// collectOutput moves a COMPOSITE instance's output into its positional
// slot. The value was read loop-side from a child scope that has since
// closed; the instance's drain returning is what makes the cell safe to
// read here (SRD-090.A M3b). A no-op for a leaf fan-out, whose instances
// capture through their own frames, and for one that produced nothing.
func (run *parallelRun) collectOutput(ord int) {
	if c := run.caps[ord]; c != nil && c.filled {
		run.outs.set(ord, c.value)
	}
}

// restoredStates rebuilds the per-ordinal state of a parallel iteration
// from its recorded set, defaulting to a fresh run of every instance when
// there is nothing recorded (SRD-090.A FR-7).
func restoredStates(seed *checkpoint.IterationRecord, n int) []string {
	states := make([]string, n)
	for i := range states {
		states[i] = instanceRunning
	}

	if seed == nil {
		return states
	}

	for _, inst := range seed.Instances {
		if inst.Ordinal < 0 || inst.Ordinal >= n {
			continue
		}

		states[inst.Ordinal] = inst.State
	}

	return states
}

// The states one instance of an iterated activity reports to the record
// (SRD-090.A FR-6). A leaf instance is never `waiting` while an iterated
// waiting activity is refused at build time (FR-10) — SRD-090.B adds it
// with the registration ownership that makes it correct.
const (
	instanceRunning   = "running"
	instanceCompleted = "completed"
)

// postPosition tells the loop's iteration mirror which instances are live.
// A leaf opens no scope and spawns no track, so neither the drain protocol
// nor the track table can show the capture anything — this post is the only
// source, and its roundtrip is the fence that makes the read loop-safe.
func (d *iterDecorator) postPosition(
	ctx context.Context, completed int, states []string,
) error {
	insts := make([]checkpoint.IterationInstance, 0, len(states))

	for ord, st := range states {
		insts = append(insts,
			checkpoint.IterationInstance{Ordinal: ord, State: st})
	}

	_, err := d.t.instance.scopeExchange(ctx, scopeRequest{
		op:        scopeIterPost,
		host:      d.t,
		node:      d.step.node,
		iterKind:  d.iterKind(),
		completed: completed,
		insts:     insts,
	})

	return err
}

// awaitParallel runs the N-of-N barrier: it takes each completion in
// arrival order, advances the counts, throws the behavior event and tests
// the completionCondition — canceling the instances still running when it
// fires (§2.7). It drains ALL N reports whatever happens, so no instance
// goroutine is left writing into a run nobody is reading.
// parallelBarrier is the N-of-N barrier's running state: how many instances
// have completed or been terminated, and the first failure if one happened.
//
// A struct rather than locals so the per-report handling can be its own
// method — the loop that drives it decides only whether a report is a
// failure, which keeps both readable.
type parallelBarrier struct {
	d   *iterDecorator
	err error
	run parallelRun

	completed  int
	terminated int
	stopping   bool
}

// fail records a mid-barrier failure the way a fired completionCondition is
// recorded, and for the reason the stop path already states: the barrier must
// take all launched reports whatever happens. Returning early left the
// still-running instances' scopes open — an instance cannot close its own
// scope on the way out, since what wakes it IS the canceled context, so the
// teardown belongs to whoever canceled (SRD-090.A M4c).
//
// The first failure wins; later ones are already downstream of it.
func (b *parallelBarrier) fail(ctx context.Context, err error) {
	if b.err != nil {
		return
	}

	b.err = err
	b.stopping = true

	if terr := b.d.stopRemaining(ctx, b.run); terr != nil {
		b.err = terr
	}
}

// took applies ONE completed instance's report: its output is staged, the
// position posted, and the completionCondition consulted. Any failure becomes
// the run's, through fail, rather than abandoning the barrier.
func (b *parallelBarrier) took(
	ctx context.Context, it miIterator, ord int,
) {
	b.completed++
	b.run.states[ord] = instanceCompleted

	// ACCOUNTED FOR, so the identity can go: this ordinal now records as
	// completed, and a later pass of the same activity mints its own. Held
	// until here so the two never disagree in a checkpoint (deliverCompletion
	// says the same from the other side).
	b.d.dropTaskID(ord)
	b.d.t.forgetTaskID(ord)

	b.run.collectOutput(ord)

	if err := b.run.outs.stage(ctx, b.d.t.miState, ord); err != nil {
		b.fail(ctx, err)

		return
	}

	// a completed instance IS the observable transition (ADR-033 §2.2): the
	// whole N-instance run is one step execution emitting no track events, so
	// without this post the position never persists (SRD-090.A FR-6).
	if err := b.d.postPosition(ctx, b.completed, b.run.states); err != nil {
		b.fail(ctx, err)

		return
	}

	if b.err != nil || b.stopping {
		return
	}

	stop, err := b.d.parallelStep(ctx, it, b.run.n, b.completed, b.terminated)
	if err != nil {
		b.fail(ctx, err)

		return
	}

	if stop {
		b.stopping = true

		// a teardown failure becomes the RUN's error rather than an early
		// return, for the same reason fail exists.
		b.err = b.d.stopRemaining(ctx, b.run)
	}
}

// parkInstances parks every instance's wait, one at a time on the decorator's
// own goroutine, and reports which of them ended up holding one.
//
// All N are parked BEFORE any is applied, because that is what "parallel"
// means for a Multi-Instance holding work: every instance is outstanding at
// once — N tasks in the distributor's inbox, N people able to act.
func (d *iterDecorator) parkInstances(
	insts map[int]activityExec, step *stepInfo,
) (map[int]bool, error) {
	parked := map[int]bool{}

	for _, ord := range sortedOrdinals(insts) {
		le, leaf := insts[ord].(*nodeExec)
		if !leaf {
			continue
		}

		if err := d.t.checkNodeTypeFor(le, step.node, false); err != nil {
			return nil, err
		}

		// asked of the EXECUTION, which owns its own wait (ADR-025 §2.13),
		// and of the subscription set for an event wait — never of anything
		// a delivery can clear underneath us.
		if le.parked.Load() || d.waitingFor(ord) {
			parked[ord] = true
		}
	}

	return parked, nil
}

// launchAll runs every instance on a goroutine of its own — the shape for
// instances that hold no wait, and for a composite's child scopes.
func (d *iterDecorator) launchAll(ctx context.Context, run parallelRun) {
	for ord, e := range run.execs {
		go func(ord int, e activityExec) {
			_, err := e.run(ctx)

			// the report is outstanding until the barrier has ACCOUNTED for
			// it, not merely received it: the activity's recorded position
			// advances in took, and a release between the two would persist a
			// checkpoint that has not heard about this instance.
			d.delivering()

			run.done <- instanceDone{ord: ord, err: err}
		}(ord, e)
	}
}

// classifyPass classifies the node for the execution running THIS pass, so a
// waiting node parks as that pass rather than as the activity — see runPass.
//
// A composite instance is not a node execution and answers through its scope,
// so only a leaf's executor is named.
func (d *iterDecorator) classifyPass(e activityExec, step *stepInfo) error {
	if le, leaf := e.(*nodeExec); leaf {
		return d.t.checkNodeTypeFor(le, step.node, false)
	}

	return d.t.checkNodeType(step.node, false)
}

// sortedOrdinals returns the instance set's ordinals in ascending order, so a
// fan-out parks and announces its tasks in a reproducible order — two runs of
// one model must not disagree about which instance was offered first.
func sortedOrdinals(insts map[int]activityExec) []int {
	ords := make([]int, 0, len(insts))
	for ord := range insts {
		ords = append(ords, ord)
	}

	sort.Ints(ords)

	return ords
}

// applyParallel is the leaf fan-out's N-of-N barrier, run as SERIAL
// APPLICATION on the decorator's goroutine (ADR-025 §2.15a).
//
// It waits for deliveries, and applies each one where it arrives: bind the
// payload into that instance's frame, run its node, record its output. One at
// a time, so the node — which is the token's, shared by every instance — is
// only ever touched by one execution.
//
// A RELEASE is not a failure: the loop taking the track away leaves every
// outstanding approval where it is, in the distributor's inbox, and the
// activity resumes from its recorded position when the instance comes back.
func (d *iterDecorator) applyParallel(
	ctx context.Context, it miIterator, run parallelRun, step *stepInfo,
) ([]*flow.SequenceFlow, error) {
	t := d.t
	b := &parallelBarrier{d: d, run: run, completed: run.n - run.launched}
	outstanding := run.launched

	for outstanding > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()

		case <-t.dehydrateCh:
			t.updateState(TrackDehydrated)

			return nil, errStopped

		case <-d.deliveries():
			for _, dlv := range d.takeDeliveries() {
				e, leaf := run.execs[dlv.ord].(*nodeExec)
				if !leaf {
					continue // an ordinal nothing is running — a late drop
				}

				if err := d.applyOne(ctx, it, b, step, e, dlv); err != nil {
					return nil, err
				}

				outstanding--
			}
		}

		if b.err != nil {
			return nil, b.err
		}
	}

	return d.finishParallel(it, run, b, step)
}

// applyOne applies ONE instance's completion, start to finish, before any
// other is looked at.
func (d *iterDecorator) applyOne(
	ctx context.Context,
	it miIterator,
	b *parallelBarrier,
	step *stepInfo,
	e *nodeExec,
	dlv instanceDelivery,
) error {
	t := d.t

	// the node is handed the delivery and then run for THIS instance, with
	// nothing in between — which is what makes the buffer it keeps for the
	// completion safe (activities.UserTask.completedOutputs).
	d.delivering()
	defer d.delivered()

	if err := t.deliver(ctx, dlv.def, e); err != nil {
		b.fail(ctx, err)

		return nil
	}

	_, err := t.executeNodeAs(ctx, step, activityInstance{
		exec:     e,
		local:    e.local,
		capture:  e.capture,
		received: e.received,
	})

	e.received = nil

	// its wait is over and its node has run: the instance is neither parked
	// nor executing from here (see track.instancesBusy).
	e.parked.Store(false)
	e.finished.Store(true)

	if err != nil {
		if errors.Is(err, errStopped) || errors.Is(err, errDehydrated) {
			return err
		}

		b.fail(ctx, err)

		return nil
	}

	b.took(ctx, it, dlv.ord)

	return nil
}

func (d *iterDecorator) awaitParallel(
	ctx context.Context, it miIterator, run parallelRun, step *stepInfo,
) ([]*flow.SequenceFlow, error) {
	t := d.t

	// the instances a restore found already complete count as completed
	// from the start: their outputs are in the restored staging, and the
	// §2.9 attributes must not report them as still to come.
	b := &parallelBarrier{d: d, run: run, completed: run.n - run.launched}

	released := false

	for range run.launched {
		res := <-run.done

		if res.err != nil {
			// THE LOOP RELEASED THE ACTIVITY (SRD-071 FR-1). Every instance
			// is being taken away, not failing: the release already set
			// TrackDehydrated, each wait is externalized to its holder, and
			// the parked work stays in the distributor's inbox.
			//
			// Failing here instead would cancel the instances that had not
			// woken yet, and a canceled instance takes awaitTrigger's
			// ctx.Done() branch — which sets TrackCanceled OVER the
			// TrackDehydrated the first one set. The track then ends as
			// evEnded rather than evDehydrated, and cleanupTask withdraws
			// every task the fan-out had announced: N people lose the work
			// in their inbox because the engine put the instance to sleep.
			//
			// The state is what distinguishes a release from a cancellation
			// — errStopped alone cannot, since it covers both.
			if errors.Is(res.err, errStopped) &&
				t.inState(TrackDehydrated) {
				released = true

				d.delivered()

				continue
			}

			// an instance WE stopped is terminated, not failed: it reports
			// the cancellation of the context this decorator closed. A
			// cancellation from above is the caller's, and faults like any
			// other error.
			if b.stopping && ctx.Err() == nil {
				b.terminated++

				d.delivered()

				continue
			}

			b.fail(ctx, res.err)

			continue
		}

		b.took(ctx, it, res.ord)
		d.delivered()
	}

	if b.err != nil {
		return nil, b.err
	}

	// a released activity publishes nothing and settles no counts: it is
	// mid-flight and stays that way durably. run() unwinds on errStopped
	// without faulting the track.
	if released {
		return nil, errStopped
	}

	return d.finishParallel(it, run, b, step)
}

// finishParallel settles the activity once every instance is accounted for:
// the §2.9 counts, the durable account, the assembled output, and the one
// traversal of the activity's outgoing flow.
func (d *iterDecorator) finishParallel(
	it miIterator, run parallelRun, b *parallelBarrier, step *stepInfo,
) ([]*flow.SequenceFlow, error) {
	t := d.t

	// the terminated count is only whole once every instance has reported,
	// so the §2.9 attributes settle here (the parallel counterpart of the
	// post-cancel rebind the loop-owned barrier did).
	// PARALLEL: every instance exists from activation, so outstanding IS
	// running and the derivation satisfies both of Table 10.30's clauses.
	if err := t.bindMICounters(
		run.n, run.n-b.completed-b.terminated,
		b.completed, b.terminated); err != nil {
		return nil, err
	}

	// and the durable account, which outlives the activity the counts end
	// with (ADR-025 §2.9.2).
	d.recordIteration(run.n, b.completed, b.terminated)

	if err := it.publishOutput(t); err != nil {
		return nil, err
	}

	t.miState = nil
	t.setLoopCounter(0)
	t.updateState(TrackProcessStepResults)

	// the activity's declared outgoing flow is followed once, on exit —
	// executing the node IS the activity, and the instances already did.
	return step.node.Outgoing(), nil
}

// sequentialStep publishes the post-drain counts, throws the behavior event
// and evaluates the completionCondition — the sequential twin of
// parallelStep, in the same order: bind, throw, evaluate.
//
// The ORDER is the point (SRD-090.A M3h). SRD-055 FR-11 and §4.3 both
// prescribe the rebind BEFORE the evaluation, so the condition sees the
// instance that just completed — "evaluated every time an instance
// completes" (§13.3.7). This path evaluated first and read what bindInstance
// published at the START of the pass, one completion behind, so
// `numberOfCompletedInstances >= 2` stopped a five-instance activity after
// THREE. The parallel path never had the defect; this is its order.
//
// active is 0 throughout: the pass has drained and the next one binds its
// own 1 (ADR-025 §2.9 — a sequential activity honors the cap).
func (d *iterDecorator) sequentialStep(
	ctx context.Context, it miIterator, n int,
) (bool, error) {
	t, mi, step := d.t, d.mi, d.step

	if err := t.bindMICounters(n, 0, t.miState.completed, 0); err != nil {
		return false, err
	}

	d.recordIteration(n, t.miState.completed, 0)

	if err := t.throwMIBehavior(
		ctx, mi, step.node, t.miState.completed); err != nil {
		return false, err
	}

	if t.miState.completed >= n || mi.CompletionCondition() == nil {
		return false, nil
	}

	met, err := it.evalCompletion(ctx, t, step.node)
	if err != nil || !met {
		return false, err
	}

	// a fired completionCondition CANCELS the instances that will now never
	// run, and the spec counts those as terminated; publishing 0 left a
	// terminal state whose counts did not add up to n (SRD-090.A M3g).
	if err := t.bindMICounters(
		n, 0, t.miState.completed, n-t.miState.completed); err != nil {
		return false, err
	}

	d.recordIteration(n, t.miState.completed, n-t.miState.completed)

	return true, nil
}

// parallelStep is the barrier's per-completion work: publish the running
// §2.9 counts, throw this completion's behavior event, then test the
// completionCondition. It reports whether the condition fired.
func (d *iterDecorator) parallelStep(
	ctx context.Context, it miIterator, n, completed, terminated int,
) (bool, error) {
	t, mi := d.t, d.mi

	// PARALLEL, as above: outstanding is running.
	if err := t.bindMICounters(
		n, n-completed-terminated, completed, terminated); err != nil {
		return false, err
	}

	if err := t.throwMIBehavior(ctx, mi, d.step.node, completed); err != nil {
		return false, err
	}

	if completed >= n || mi.CompletionCondition() == nil {
		return false, nil
	}

	return it.evalCompletion(ctx, t, d.step.node)
}

// instanceFor builds instance ord: its OWN step (siblings must not race on
// one step's state), its own frame-local data (FR-4), and — when the
// activity assembles output — the capture that takes its result before the
// commit makes the output's name a shared one.
//
// A COMPOSITE instance's capture is returned alongside the executor rather
// than read back off it: the fan-out has to collect those loop-side, and the
// builder is the one place that knows whether this instance has one. Asking
// the executor instead would mean asking what KIND of executor it is, which
// is the one question the composition exists to make unanswerable (ADR-025
// §2.13a). A leaf instance captures through its own frame and returns nil.
func (d *iterDecorator) instanceFor(
	ctx context.Context, ord int, outs *instanceOutputs,
) (activityExec, *instanceCapture, error) {
	st := d.t.miState

	if d.composite {
		return d.compositeInstanceFor(ctx, ord)
	}

	local, err := iterationLocals(ctx, st, ord,
		d.t.scopePath.String(), d.iterKind(), d.step.node)
	if err != nil {
		return nil, nil, err
	}

	e := &nodeExec{
		t:     d.t,
		step:  &stepInfo{node: d.step.node, inFlow: d.step.inFlow},
		ord:   ord,
		local: local,
	}

	if st.staging != nil {
		e.capture = func(f *scope.Frame) error {
			outs.take(ctx, ord, f, st.outputItem)

			return nil
		}
	}

	return e, nil, nil
}

// compositeInstanceFor builds instance ord of a PARALLEL composite: its own
// child scope, the per-instance data published there, and the cell its
// output is read into before that scope closes.
//
// The three fields are what separate a fanned-out instance from a sequential
// pass, which opens the node's own scope with none of them (SRD-090.A M3b).
// The loop applies them; the executor only carries them, because deriving
// the segment loop-side would move the sequential path's data paths.
func (d *iterDecorator) compositeInstanceFor(
	ctx context.Context, ord int,
) (activityExec, *instanceCapture, error) {
	st := d.t.miState

	e := newScopeExec(d.t, &stepInfo{
		node: d.step.node, inFlow: d.step.inFlow,
	}, ord)

	e.iterKind = d.iterKind()
	e.segment = scopeSegment(d.step.node) + "-" + strconv.Itoa(ord)

	// the 0-based loopCounter, and the split input item when the iteration
	// is collection-driven — bound at the instance's own scope, which is
	// what makes them concurrency-safe where the sequential slice's
	// host-scope binds are not.
	e.binds = []miBinding{{name: data.LoopCounterName, value: ord}}

	if st != nil && st.collection != nil {
		elem, err := st.collection.GetAt(ctx, ord)
		if err != nil {
			return nil, nil, err
		}

		e.binds = append(e.binds,
			miBinding{name: st.inputItem, value: elem})
	}

	if st != nil && st.staging != nil {
		e.capture = &instanceCapture{item: st.outputItem}
	}

	return e, e.capture, nil
}

// iterationLocals builds instance ord's own data: the 0-based loopCounter
// and, for a collection-driven Multi-Instance, the element split off at
// that ordinal. They are bound frame-local, so the instances of one
// activity cannot overwrite each other's (SRD-090.A FR-4).
func iterationLocals(
	ctx context.Context, st *miState, ord int,
	scopePath, kind string, node flow.Node,
) ([]data.Data, error) {
	counter, err := data.ReadyValueParameter(
		data.LoopCounterName, values.NewVariable(ord))
	if err != nil {
		return nil, err
	}

	local := make([]data.Data, 0, 5)
	local = append(local, counter)

	// the engine's own names for the same execution, built by the one
	// builder every publication path shares (iterationvars.go).
	vars, err := iterationVars(scopePath, kind, node, ord)
	if err != nil {
		return nil, err
	}

	local = append(local, vars...)

	if st.collection == nil {
		return local, nil
	}

	elem, err := st.collection.GetAt(ctx, ord)
	if err != nil {
		return nil, err
	}

	item, err := data.ReadyValueParameter(
		st.inputItem, values.NewVariable(elem))
	if err != nil {
		return nil, err
	}

	return append(local, item), nil
}

// instanceOutputs holds each parallel instance's captured output until the
// decorator stages it. An instance writes ONLY its own slot, from its own
// goroutine, and the decorator reads that slot only after the instance's
// completion arrives over the channel — so the handoff is ordered without a
// lock, and the staging collection (which is not safe for concurrent use)
// is written on the decorator's goroutine alone.
type instanceOutputs struct {
	values []any
	filled []bool
}

func newInstanceOutputs(n int) *instanceOutputs {
	return &instanceOutputs{values: make([]any, n), filled: make([]bool, n)}
}

// take records instance ord's declared output, resolved through its own
// frame. Frame-first resolution is the point: the value a node produced is
// still the instance's own there, whereas the same name in the shared
// container scope is whatever sibling committed last.
func (o *instanceOutputs) take(
	ctx context.Context, ord int, f *scope.Frame, name string,
) {
	d, err := f.GetData(name)
	if err != nil {
		// an instance that produced no output leaves its slot nil, as a
		// canceled one does (§2.7) — an activity whose output is optional
		// is not an error here.
		return
	}

	o.set(ord, d.Value().Get(ctx))
}

// set records instance ord's output directly. It is how a COMPOSITE
// instance reports: its value was read loop-side from a child scope that
// has since closed, so there is no frame left to resolve it through.
func (o *instanceOutputs) set(ord int, v any) {
	o.values[ord] = v
	o.filled[ord] = true
}

// stage writes instance ord's captured output into its positional slot. A
// no-op when the activity assembles no output, or when the instance
// produced none — that slot keeps its nil, as a canceled one does.
func (o *instanceOutputs) stage(
	ctx context.Context, st *miState, ord int,
) error {
	if st == nil || st.staging == nil || !o.filled[ord] {
		return nil
	}

	return st.staging.SetAt(ctx, ord, o.values[ord])
}

// presizedStaging returns the staging collection pre-sized to n, carrying
// over whatever a restored position had already staged. SetAt replaces
// rather than appends, so an out-of-order parallel completion needs its
// slot to exist before it writes.
func presizedStaging(
	ctx context.Context, staged *values.Array[any], n int,
) *values.Array[any] {
	sized := values.NewArray[any](make([]any, n)...)

	for i, el := range staged.GetAll(ctx) {
		if i >= n || el == nil {
			continue
		}

		if err := sized.SetAt(ctx, i, el); err != nil {
			// a slot that will not take a value it already held is not
			// recoverable here; the pre-sized array stands and the slot
			// keeps its nil, which the publish reports as a hole.
			continue
		}
	}

	return sized
}

// newNodeExec builds the executor for one instance of a leaf activity.
func newNodeExec(t *track, step *stepInfo, ordinal int) *nodeExec {
	return &nodeExec{t: t, step: step, ord: ordinal}
}

// run executes the node once, as this instance.
func (e *nodeExec) run(ctx context.Context) ([]*flow.SequenceFlow, error) {
	return e.t.executeNodeAs(ctx, e.step, activityInstance{
		exec:    e,
		local:   e.local,
		capture: e.capture,
	})
}

// awaits reports an event wait, which is the only kind a leaf can hold: a
// leaf opens no scope and owns no child instance.
//
// It reads the driving track's state because in this slice the two are one
// to one. A decorator's instances report their own (M2), and the reading
// here does not change: a parked leaf awaits an event.
func (e *nodeExec) awaits() awaitKind {
	if e.parked.Load() {
		return awaitEvent
	}

	// An execution that is alone on its track still derives from it, because
	// a node parks through paths that mark the track and never reach an
	// executor — a born-parked waiter, a restored one. A plain activity and
	// a sequential pass are both alone in that sense, so the reading is
	// exact for them.
	if !e.concurrent && e.t.inState(TrackWaitForEvent) {
		return awaitEvent
	}

	return awaitNothing
}

// subscriber: a leaf execution is its own subscriber, so the per-trigger
// rule in armWaiters stands unchanged (SRD-090.B FR-1).
func (e *nodeExec) subscriber() activitySubscriber { return nil }

// state reports this instance in the iteration vocabulary.
func (e *nodeExec) state() instanceState {
	a := e.awaits()

	return instanceState{
		ordinal: e.ord,
		await:   a,
		done:    a == awaitNothing && e.step.state == StepEnded,
	}
}

// runPass executes ONE instance of a sequential activity through the
// executor the decorator holds for it. It lives on the decorator because the
// decorator OWNS the iteration (ADR-025 §2.13) — leaving it on the track
// left the driver split across both types, so the decorator delegated back to
// the thing it was replacing. Originally SRD-086 FR-1's pass: bind,
// run the instance, take its output, post the pass to the loop's iteration
// mirror, evaluate the completionCondition with pass-start counts, and throw
// the behavior event. stop reports a fired condition, posted to the mirror
// over the protocol (SRD-082 FR-2).
//
// The pass is the same for a leaf and a composite except in what happens
// AROUND the instance's run, and those three points are guarded here rather
// than duplicated into a second driver — see each guard for why it exists.
func (d *iterDecorator) runPass(
	ctx context.Context, it miIterator, i, n int,
) ([]*flow.SequenceFlow, bool, error) {
	t, step := d.t, d.step

	t.setLoopCounter(i)

	// bind loopCounter, the §2.9 counts and inputItem=collection[i] at
	// the host scope — the activity's frame resolves them by walk-up,
	// exactly as a seeded composite body does.
	if err := it.bindInstance(ctx, t, i); err != nil {
		return nil, false, err
	}

	// re-arm the step for another execution (the same idiom a Standard Loop
	// pass uses):
	// finalizeNodeExecution ended the previous pass. A composite executes
	// its node ONCE, on exit, so its step is never re-armed — its
	// instances open scopes rather than re-running it.
	// runInstance stores the instance before calling this, and nothing else
	// calls it — so the handle is always set here, where the two other
	// Load sites guard because they answer the LOOP, which can ask between
	// instances. Failing loudly beats a nil dereference three frames down
	// if a future caller breaks that.
	h := d.live.Load()
	if h == nil {
		return nil, false, errs.Invariant(
			"no live instance for %q at ordinal %d", d.step.node.Name(), i)
	}

	if !d.composite {
		step.state = StepCreated

		// re-classify the node for THIS pass (SRD-090.B FR-2). Arming and
		// parking are keyed to a token arriving, and an in-place iteration
		// arrives once — so without this the pass runs a waiting node
		// without waiting, which is #313's defect. The decorator does it
		// because the decorator is what re-runs the node; the subscription
		// bookkeeping underneath is idempotent for an ordinal already
		// recorded, so a node that does not wait pays nothing.
		//
		// FOR THIS PASS'S EXECUTION, not for the track. An iterated activity
		// is parked by its instances rather than by the arrival that reaches
		// it — the arrival happens once however many passes run — so a
		// classification that named no execution would be skipped as that
		// arrival, and the pass would run its waiting node without waiting.
		// Naming the execution is also what gives each pass its own
		// parked-work identity: three passes over a User Task are three
		// offers, one at a time (ADR-020 §2.12).
		if err := d.classifyPass(h.e, step); err != nil {
			return nil, false, err
		}
	}

	flows, err := h.e.run(ctx)

	if err != nil {
		return nil, false, err
	}

	if err := d.captureOutput(ctx, i); err != nil {
		return nil, false, err
	}

	t.miState.completed++

	// a sequential decorator holds at most one instance, so its live set
	// is the pass about to run — the completed count IS the position, and
	// restore resumes at it (seedSequentialStart).
	//
	// A COMPOSITE does not post: the loop already advanced the mirror from
	// the drain it delivered (markIterDrain), which is strictly earlier
	// than this point. Posting here as well would be harmless but would
	// widen the window in which a capture sees a drained instance still
	// counted as running.
	if !d.composite {
		if err := d.postPosition(ctx, t.miState.completed, nil); err != nil {
			return nil, false, err
		}
	}

	// serr, not err: reusing the name would extend the run() error's live
	// range to the end of the function and turn the two `if err :=` guards
	// above into shadows.
	stop, serr := d.sequentialStep(ctx, it, n)
	if serr != nil {
		return nil, false, serr
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
//
// A COMPOSITE instance's output is not here to be read: it lives in that
// instance's child scope, which is already closed by the time this returns.
// The loop takes it from there before closing (captureSequentialOutput,
// §4.2) — the only moment it exists.
func (d *iterDecorator) captureOutput(ctx context.Context, i int) error {
	if d.composite {
		return nil
	}

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
