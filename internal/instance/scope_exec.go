package instance

import (
	"context"
	"errors"
	"sort"
	"sync/atomic"

	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// errDehydrated unwinds a track whose executor the loop released while it
// held something open (SRD-090.A FR-8). It is NOT a failure and not a
// cancellation: the activity is mid-flight and stays that way, durably, and
// the run loop returns without touching the terminal state the release
// already set.
var errDehydrated = errors.New("the loop released this executor")

// scopeExec is the sub-process realization of activityExec: it runs ONE
// iteration of a COMPOSITE activity, and an instance of a composite activity
// is a child scope (ADR-025 §2.13, ADR-023 §2.4).
//
// It executes no node. A composite's work IS its body, and the body's tokens
// are ordinary tracks the loop seeds into the opened scope (FR-3) — so this
// executor's whole job is that scope's lifetime: ask the single-writer loop
// to open it, park until the loop reports it drained, and say meanwhile that
// the instance awaits a scope rather than that it is working (FR-8). That
// last part is why the mechanism becomes an executor at all: "parked for a
// child's drain" was previously indistinguishable, from outside the runner's
// own stack, from "executing".
type scopeExec struct {
	t    *track
	step *stepInfo

	// drain is closed by the loop when this iteration's scope has drained.
	// It belongs to the INSTANCE rather than to the host track, which is
	// what lets N of them wait at once (SRD-090.A M3b).
	drain chan struct{}

	// segment, binds and capture are set only when this iteration is one of
	// N fanned out in parallel: its own scope path, the per-iteration data
	// published there, and the cell its output is read into. A sequential
	// pass and a plain composite leave all three zero and open the node's
	// own `sp-<id>` with nothing extra (SRD-090.A M3b).
	capture *instanceCapture
	segment string
	// iterKind names the iteration shape this iteration belongs to, for the
	// loop's position mirror (SRD-090.A FR-6). Empty for a plain composite,
	// which iterates not at all.
	//
	// Supplied by the decorator, for the same reason as iterating: the loop
	// used to derive it by asking the node (iterKindOf, on the way in),
	// which is a driver testing what kind of iteration a node drives — the
	// knowledge the decorator owns (FR-11).
	iterKind string
	binds    []miBinding

	// parked is written by this iteration's own goroutine and read by the
	// LOOP goroutine, so it is atomic. A reader wants the CURRENT answer
	// and never a consistent pair with anything else, which is what makes
	// a single word enough — and a composite iteration awaits exactly one
	// thing, so a flag says all there is to say.
	parked atomic.Bool

	// exits marks this executor as the WHOLE activity rather than one of
	// N: a plain composite, which has no decorator above it to follow the
	// outgoing flow once the instances are done (SRD-090.A M3c). It is the
	// executor's own business, not a caller's type test — which is what
	// keeps executeStep at one decision (FR-2, FR-11).
	exits bool

	ord int
}

// newScopeExec builds the executor for one instance of a composite activity,
// held by a decorator that follows the activity's exit flow itself.
func newScopeExec(t *track, step *stepInfo, ordinal int) *scopeExec {
	return &scopeExec{
		t:     t,
		step:  step,
		ord:   ordinal,
		drain: make(chan struct{}),
	}
}

// newPlainScopeExec builds the executor for a NON-iterated composite —
// iteration zero of one, and the whole activity — so it follows the exit
// flow itself when its scope drains.
func newPlainScopeExec(t *track, step *stepInfo) *scopeExec {
	e := newScopeExec(t, step, 0)
	e.exits = true

	return e
}

// run opens this iteration's child scope and awaits its drain.
//
// It returns NO flows. A composite activity is one token's step however many
// instances run it, and the flows are followed once by whatever drives the
// instances — the decorator on exit, or executeStep for the single iteration
// of a non-iterated composite.
func (e *scopeExec) run(ctx context.Context) ([]*flow.SequenceFlow, error) {
	if _, err := e.t.instance.scopeRoundtrip(ctx, scopeRequest{
		op: scopeOpen, host: e.t, node: e.step.node, drain: e.drain,
		segment: e.segment, binds: e.binds, capture: e.capture,
		ordinal: e.ord,
		// the whole activity's open is a lifecycle transition worth a
		// checkpoint; one pass of a decorated one is interior to a step
		// and persists through the decorator's position post instead.
		persist: e.exits,
		// an instance held by a decorator iterates; the whole activity,
		// running once, does not. The executor knows which it is without
		// asking anything about the node (SRD-090.A FR-11).
		iterating:   !e.exits,
		iterKind:    e.iterKind,
		factOrdinal: e.factOrdinal(),
	}); err != nil {
		return nil, err
	}

	// the scope is open from HERE, not from the request: the loop opens it
	// on its own goroutine and the roundtrip is what fences that against
	// this read. Cleared on the way out, including the error path — a
	// faulted iteration awaits nothing.
	e.parked.Store(true)
	defer e.parked.Store(false)

	// the token says what it is doing (ADR-025 §2.13b.1e): this activity has
	// forked into a child scope and is waiting for the body to drain — not
	// executing, which is what it reported before, and not waiting for an
	// event either, since nothing external will wake it.
	//
	// Only the WHOLE activity says so. One instance of N is inside an
	// iterating track, and the state of a token is what its activity is
	// doing, not what one of its instances is.
	if e.exits {
		e.t.updateState(TrackHostingScope)
	}

	if err := e.awaitDrain(ctx); err != nil {
		return nil, err
	}

	// A composite's outgoing flow is selected by RE-RUNNING its node once
	// the body is done — the body ran in the child scope, and the node
	// execution is what picks the flow (the same rule a decorator's exit
	// follows for a composite). Only the whole activity does this: one
	// iteration of N leaves it to the decorator, which does it once.
	if e.exits {
		return e.t.executeNode(ctx, e.step)
	}

	return nil, nil
}

// factOrdinal is the ordinal this iteration's scope lifecycle facts carry,
// or -1 to omit the attribute (SRD-054 FR-11, SRD-055 FR-13).
//
// A FANNED-OUT iteration reports its OWN — the host's loopCounter is shared
// by all N and stands still for the whole fan-out, so it cannot name any of
// them. A serial pass has no ordinal of its own and the host's pass counter
// IS its position. The whole activity, running once, has no pass to report.
func (e *scopeExec) factOrdinal() int {
	switch {
	case e.exits:
		return -1

	case e.segment != "":
		return e.ord
	}

	return e.t.loopCounterSnap()
}

// awaitDrain parks this iteration until the loop reports its scope drained,
// honoring cancellation and iteration shutdown so a mid-pass interrupt or
// terminate unblocks it rather than hanging (SRD-054 NFR-4).
//
// It waits on the instance's OWN channel. The host track's evtCh — which the
// single-park protocol used — could only ever serve one waiter, so a second
// iteration of the same activity would have consumed the first one's drain.
func (e *scopeExec) awaitDrain(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()

	case <-e.t.instance.loopDone:
		// the loop stopped mid-pass; the scope will never drain. Reported
		// as a cancellation, which is how the evtCh close read before.
		return context.Canceled

	case <-e.t.dehydrateCh:
		// A DRAIN THAT ALREADY HAPPENED WINS. Both channels are closed by
		// the loop, and it can close them back to back: completeScope
		// closes the drain, and the loop tail then runs maybeDehydrate
		// while this goroutine has not yet been scheduled — so it still
		// reads as parked and gets released too. With both ready, select
		// picks at random, and picking the release would discard a
		// completion that will never be signaled again: the restored host
		// would re-attach to a scope the loop had already torn down.
		select {
		case <-e.drain:
			return nil
		default:
		}

		// the loop released this host (SRD-090.A FR-8): every live track,
		// this iteration's body included, is parked on a held wait, so the
		// whole iteration is going away and the drain will not arrive in
		// this process. The scope is NOT closed and the body is NOT torn
		// down — both are durable, and a restore re-enters this decorator
		// at its recorded position, re-attaching to the scope it left.
		e.t.updateState(TrackDehydrated)

		return errDehydrated

	case <-e.drain:
		return nil
	}
}

// awaits reports the drain this iteration is parked for, which is the only
// kind a composite iteration can hold: the body's own waits belong to the
// body's tracks, and they answer for themselves (FR-8).
func (e *scopeExec) awaits() awaitKind {
	if e.parked.Load() {
		return awaitScope
	}

	return awaitNothing
}

// subscriber: a composite iteration opens a scope and registers nothing —
// the body's own tracks own their waits (SRD-090.B FR-1).
func (e *scopeExec) subscriber() activitySubscriber { return nil }

// iterationScopesOf returns the still-open INSTANCE scopes this host fanned
// out, in ordinal order — never its own serial pass's scope, which is one
// scope reused and answers to the host's own counter.
//
// It is the one lookup that answers both of the questions the retired
// loop-owned group barrier used to answer: which scopes a fired
// completionCondition must tear down, and which an interrupting boundary
// must. Keeping them one
// lookup is the point — two would drift, and the group existed partly
// because there was no way to ask this at all.
//
// The order matters for the same reason handleReAttach sorted its pending
// set: live completions of concurrent instances carry no defined order, but
// a teardown feeds the ledger the reverse-order compensation sweep reads,
// so it must be reproducible.
func (ls *loopState) iterationScopesOf(host *track) []scope.DataPath {
	paths := make([]scope.DataPath, 0, len(ls.scopes))

	for p, entry := range ls.scopes {
		if entry.instance && entry.host == host {
			paths = append(paths, p)
		}
	}

	sort.Slice(paths, func(i, j int) bool {
		return ls.scopes[paths[i]].ordinal < ls.scopes[paths[j]].ordinal
	})

	return paths
}

// handleCancelIterations tears down every still-open iteration scope of the
// requesting host and reports the count, which the decorator's barrier
// carries as its terminated tally (SRD-056.A §2.9). Runs on the loop
// goroutine.
func (ls *loopState) handleCancelIterations(req scopeRequest) {
	paths := ls.iterationScopesOf(req.host)

	for _, p := range paths {
		ls.cancelScope(p, observability.PhaseCanceled)
	}

	req.reply <- scopeReply{terminated: len(paths)}
}

// captureIterationOutput reads the opening iteration's declared output item
// from its draining child scope into the cell that iteration is waiting on.
//
// It runs on the loop goroutine from completeScope, before the scope closes
// — the last point the data is readable.
//
// A declared outputDataItem the body did not produce FAULTS, exactly as it
// does for a sequential composite (captureSequentialOutput): the declaration
// is a contract with the body, and a silently nil slot would publish a
// collection that lies about what the instances returned. That is the one
// place a composite iteration differs from a leaf's frame capture, which
// tolerates the same absence — the leaf reads a frame that may legitimately
// hold nothing, this reads a scope the body was required to write.
func (ls *loopState) captureIterationOutput(
	ctx context.Context, entry *scopeEntry, path scope.DataPath,
) error {
	c := entry.capture
	if c == nil || c.item == "" {
		return nil
	}

	d, err := ls.inst.sc.plane.GetData(path, c.item)
	if err != nil {
		return err
	}

	c.value = d.Value().Get(ctx)
	c.filled = true

	return nil
}

// state reports this iteration in the iteration vocabulary.
func (e *scopeExec) state() iterationState {
	a := e.awaits()

	return iterationState{
		ordinal: e.ord,
		await:   a,
		done:    a == awaitNothing && e.step.state == StepEnded,
	}
}
