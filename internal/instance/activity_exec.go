package instance

import (
	"context"
	"strconv"

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
// holds several instances of one node (SRD-090.A M2), the ordinal joins that
// identity — which is why it is carried here from the start rather than
// introduced later at every call site.
type nodeExec struct {
	t    *track
	step *stepInfo

	// capture takes this instance's declared output while it is still the
	// instance's own — see activityInstance.capture.
	capture func(f *scope.Frame) error

	// local is this instance's own data, bound frame-local so a concurrent
	// sibling cannot overwrite it (SRD-090.A FR-4). nil for the single
	// instance of a plain activity, which has nothing to distinguish.
	local []data.Data

	ord int

	// inSet marks membership of a decorator-driven set: the decorator, not
	// this instance, owns the track-wide transitions.
	inSet bool
}

// execFor builds the executor that runs this node: a decorator when the node
// carries loop characteristics this slice has converted, a single instance
// otherwise. The caller does not care which — both satisfy activityExec, so
// a track drives one executor and cannot tell how many instances are behind
// it (ADR-025 §2.13).
//
// The one kind still routed by executeStep — a LEAF Standard Loop, which
// re-runs its node in place — moves here when it converts (SRD-090.B).
func execFor(t *track, step *stepInfo) activityExec {
	_, composite := step.node.(scopeHost)

	if sl := standardLoopOf(step.node); sl != nil && composite {
		return newLoopDecorator(t, step, sl)
	}

	if mi := multiInstanceOf(step.node); mi != nil {
		return newIterDecorator(t, step, mi, composite)
	}

	// A plain activity is instance zero of one — the common case kept
	// uniform rather than special-cased. A plain COMPOSITE never reaches
	// here at all: it parks on entry for the loop-driven scope re-entry
	// (enterComposite), so its single instance is not executed from a
	// step.
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
	live activityExec

	// seed is the restored executor set this activity resumes from, taken
	// from the track at the start of the run (SRD-090.A FR-7).
	seed *checkpoint.IterationRecord

	composite bool
}

// newIterDecorator builds the decorator for an iterated activity.
func newIterDecorator(
	t *track, step *stepInfo, mi multiInstance, composite bool,
) *iterDecorator {
	return &iterDecorator{t: t, step: step, mi: mi, composite: composite}
}

// buildInstance makes the executor for ordinal ord: a child scope for a
// composite activity, an execution of the node for a leaf.
func (d *iterDecorator) buildInstance(ord int) activityExec {
	if d.composite {
		return newScopeExec(d.t, d.step, ord)
	}

	return newNodeExec(d.t, d.step, ord)
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
	// activity's outgoing flow, exactly once. A composite's instances
	// return no flows at all — its node has not executed yet — so the exit
	// is always the one that produces them.
	if nextFlows == nil || d.composite {
		return d.exitFlows(ctx)
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
// it, which requires building the construct the snapshot refuses. SRD-090.B
// covers that end to end, in the slice that makes the construct buildable.
func (d *iterDecorator) runInstance(
	ctx context.Context, it miIterator, i, n int,
) ([]*flow.SequenceFlow, bool, error) {
	d.live = d.buildInstance(i)

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
// is trivial while at most one instance runs (ADR-025 §2.13's
// releasability rule takes its general form when parallel instances arrive).
func (d *iterDecorator) awaits() awaitKind {
	if d.live == nil {
		return awaitNothing
	}

	return d.live.awaits()
}

// state reports the ACTIVITY's iteration state: the live instance's ordinal
// and what it is doing. Its own ordinal is 0 — the activity is one instance
// of itself from the track's point of view.
func (d *iterDecorator) state() instanceState {
	if d.live == nil {
		return instanceState{ordinal: 0, await: awaitNothing}
	}

	return d.live.state()
}

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
	// so the track-wide transitions are made here, once, and each instance
	// runs inSet (see activityInstance.inSet).
	t.updateState(TrackExecutingStep)
	t.record(TrackExecutingStep)

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

	insts := make(map[int]activityExec, n)

	// a COMPOSITE instance's output is read loop-side, into the cell the
	// executor carries; the barrier collects it once the instance reports.
	caps := make(map[int]*instanceCapture, n)

	for ord, st := range states {
		if st == instanceCompleted {
			continue
		}

		e, err := d.instanceFor(ctx, ord, outs)
		if err != nil {
			return nil, err
		}

		insts[ord] = e

		if se, ok := e.(*scopeExec); ok {
			caps[ord] = se.capture
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

	for ord, e := range insts {
		go func(ord int, e activityExec) {
			_, err := e.run(runCtx)
			done <- instanceDone{ord: ord, err: err}
		}(ord, e)
	}

	return d.awaitParallel(ctx, it, parallelRun{
		n:          n,
		done:       done,
		outs:       outs,
		caps:       caps,
		cancelRest: cancelRest,
		states:     states,
		launched:   len(insts),
	}, step)
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
		op:    scopeIterPost,
		host:  d.t,
		node:  d.step.node,
		n:     completed,
		insts: insts,
	})

	return err
}

// awaitParallel runs the N-of-N barrier: it takes each completion in
// arrival order, advances the counts, throws the behavior event and tests
// the completionCondition — canceling the instances still running when it
// fires (§2.7). It drains ALL N reports whatever happens, so no instance
// goroutine is left writing into a run nobody is reading.
func (d *iterDecorator) awaitParallel(
	ctx context.Context, it miIterator, run parallelRun, step *stepInfo,
) ([]*flow.SequenceFlow, error) {
	t := d.t

	// the instances a restore found already complete count as completed
	// from the start: their outputs are in the restored staging, and the
	// §2.9 attributes must not report them as still to come.
	completed := run.n - run.launched

	var (
		terminated int
		runErr     error
		stopping   bool
	)

	for range run.launched {
		res := <-run.done

		if res.err != nil {
			// an instance WE stopped is terminated, not failed: it
			// reports the cancellation of the context this decorator
			// closed. A cancellation from above is the caller's, and
			// faults like any other error.
			if stopping && ctx.Err() == nil {
				terminated++

				continue
			}

			if runErr == nil {
				runErr = res.err
				stopping = true

				run.cancelRest()
			}

			continue
		}

		completed++
		run.states[res.ord] = instanceCompleted

		run.collectOutput(res.ord)

		if err := run.outs.stage(ctx, t.miState, res.ord); err != nil {
			return nil, err
		}

		// a completed instance IS the observable transition (ADR-033
		// §2.2): the whole N-instance run is one step execution emitting
		// no track events, so without this post the position never
		// persists (SRD-090.A FR-6).
		if err := d.postPosition(ctx, completed, run.states); err != nil {
			return nil, err
		}

		if runErr != nil || stopping {
			continue
		}

		stop, err := d.parallelStep(ctx, it, run.n, completed, terminated)
		if err != nil {
			return nil, err
		}

		if stop {
			stopping = true

			// a teardown failure becomes the RUN's error rather than an
			// early return: this loop must take all launched reports
			// whatever happens, or an instance goroutine is left writing
			// into a run nobody reads.
			runErr = d.stopRemaining(ctx, run)
		}
	}

	if runErr != nil {
		return nil, runErr
	}

	// the terminated count is only whole once every instance has reported,
	// so the §2.9 attributes settle here (the parallel counterpart of the
	// post-cancel rebind the loop-owned barrier did).
	if err := t.bindMICounters(run.n, completed, terminated); err != nil {
		return nil, err
	}

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

// parallelStep is the barrier's per-completion work: publish the running
// §2.9 counts, throw this completion's behavior event, then test the
// completionCondition. It reports whether the condition fired.
func (d *iterDecorator) parallelStep(
	ctx context.Context, it miIterator, n, completed, terminated int,
) (bool, error) {
	t, mi := d.t, d.mi

	if err := t.bindMICounters(n, completed, terminated); err != nil {
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
func (d *iterDecorator) instanceFor(
	ctx context.Context, ord int, outs *instanceOutputs,
) (activityExec, error) {
	st := d.t.miState

	if d.composite {
		return d.compositeInstanceFor(ctx, ord)
	}

	local, err := iterationLocals(ctx, st, ord)
	if err != nil {
		return nil, err
	}

	e := &nodeExec{
		t:     d.t,
		step:  &stepInfo{node: d.step.node, inFlow: d.step.inFlow},
		ord:   ord,
		local: local,
		inSet: true,
	}

	if st.staging != nil {
		e.capture = func(f *scope.Frame) error {
			outs.take(ctx, ord, f, st.outputItem)

			return nil
		}
	}

	return e, nil
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
) (activityExec, error) {
	st := d.t.miState

	e := newScopeExec(d.t, &stepInfo{
		node: d.step.node, inFlow: d.step.inFlow,
	}, ord)

	e.segment = scopeSegment(d.step.node) + "-" + strconv.Itoa(ord)

	// the 0-based loopCounter, and the split input item when the iteration
	// is collection-driven — bound at the instance's own scope, which is
	// what makes them concurrency-safe where the sequential slice's
	// host-scope binds are not.
	e.binds = []miBinding{{name: "loopCounter", value: ord}}

	if st != nil && st.collection != nil {
		elem, err := st.collection.GetAt(ctx, ord)
		if err != nil {
			return nil, err
		}

		e.binds = append(e.binds,
			miBinding{name: st.inputItem, value: elem})
	}

	if st != nil && st.staging != nil {
		e.capture = &instanceCapture{item: st.outputItem}
	}

	return e, nil
}

// iterationLocals builds instance ord's own data: the 0-based loopCounter
// and, for a collection-driven Multi-Instance, the element split off at
// that ordinal. They are bound frame-local, so the instances of one
// activity cannot overwrite each other's (SRD-090.A FR-4).
func iterationLocals(
	ctx context.Context, st *miState, ord int,
) ([]data.Data, error) {
	counter, err := data.ReadyValueParameter(
		"loopCounter", values.NewVariable(ord))
	if err != nil {
		return nil, err
	}

	local := make([]data.Data, 0, 2)
	local = append(local, counter)

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
		local:   e.local,
		capture: e.capture,
		inSet:   e.inSet,
	})
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
	t, step, mi := d.t, d.step, d.mi

	t.setLoopCounter(i)

	// bind loopCounter, the §2.9 counts and inputItem=collection[i] at
	// the host scope — the activity's frame resolves them by walk-up,
	// exactly as a seeded composite body does.
	if err := it.bindInstance(ctx, t, i); err != nil {
		return nil, false, err
	}

	// re-arm the step for another execution (the runStandardLoop idiom):
	// finalizeNodeExecution ended the previous pass. A composite executes
	// its node ONCE, on exit, so its step is never re-armed — its
	// instances open scopes rather than re-running it.
	if !d.composite {
		step.state = StepCreated
	}

	flows, err := d.live.run(ctx)
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
