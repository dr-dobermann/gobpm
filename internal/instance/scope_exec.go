package instance

import (
	"context"
	"sync/atomic"

	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// scopeExec is the sub-process realization of activityExec: it runs ONE
// instance of a COMPOSITE activity, and an instance of a composite activity
// is a child scope (ADR-025 v.3 §2.13, ADR-023 v.3 §2.4).
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

	// drain is closed by the loop when this instance's scope has drained.
	// It belongs to the INSTANCE rather than to the host track, which is
	// what lets N of them wait at once (SRD-090.A M3b).
	drain chan struct{}

	// parked is written by this instance's own goroutine and read by the
	// LOOP goroutine, so it is atomic. A reader wants the CURRENT answer
	// and never a consistent pair with anything else, which is what makes
	// a single word enough — and a composite instance awaits exactly one
	// thing, so a flag says all there is to say.
	parked atomic.Bool

	// segment, binds and capture are set only when this instance is one of
	// N fanned out in parallel: its own scope path, the per-instance data
	// published there, and the cell its output is read into. A sequential
	// pass and a plain composite leave all three zero and open the node's
	// own `sp-<id>` with nothing extra (SRD-090.A M3b).
	segment string
	binds   []miBinding
	capture *instanceCapture

	ord int
}

// newScopeExec builds the executor for one instance of a composite activity.
func newScopeExec(t *track, step *stepInfo, ordinal int) *scopeExec {
	return &scopeExec{
		t:     t,
		step:  step,
		ord:   ordinal,
		drain: make(chan struct{}),
	}
}

// run opens this instance's child scope and awaits its drain.
//
// It returns NO flows. A composite activity is one token's step however many
// instances run it, and the flows are followed once by whatever drives the
// instances — the decorator on exit, or executeStep for the single instance
// of a non-iterated composite.
func (e *scopeExec) run(ctx context.Context) ([]*flow.SequenceFlow, error) {
	if _, err := e.t.instance.scopeRoundtrip(ctx, scopeRequest{
		op: scopeOpen, host: e.t, node: e.step.node, drain: e.drain,
		segment: e.segment, binds: e.binds, capture: e.capture,
		ordinal: e.ord,
	}); err != nil {
		return nil, err
	}

	// the scope is open from HERE, not from the request: the loop opens it
	// on its own goroutine and the roundtrip is what fences that against
	// this read. Cleared on the way out, including the error path — a
	// faulted instance awaits nothing.
	e.parked.Store(true)
	defer e.parked.Store(false)

	if err := e.awaitDrain(ctx); err != nil {
		return nil, err
	}

	return nil, nil
}

// awaitDrain parks this instance until the loop reports its scope drained,
// honoring cancellation and instance shutdown so a mid-pass interrupt or
// terminate unblocks it rather than hanging (SRD-054 NFR-4).
//
// It waits on the instance's OWN channel. The host track's evtCh — which the
// single-park protocol used — could only ever serve one waiter, so a second
// instance of the same activity would have consumed the first one's drain.
func (e *scopeExec) awaitDrain(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()

	case <-e.t.instance.loopDone:
		// the loop stopped mid-pass; the scope will never drain. Reported
		// as a cancellation, which is how the evtCh close read before.
		return context.Canceled

	case <-e.drain:
		return nil
	}
}

// awaits reports the drain this instance is parked for, which is the only
// kind a composite instance can hold: the body's own waits belong to the
// body's tracks, and they answer for themselves (FR-8).
func (e *scopeExec) awaits() awaitKind {
	if e.parked.Load() {
		return awaitScope
	}

	return awaitNothing
}

// captureInstanceOutput reads the opening instance's declared output item
// from its draining child scope into the cell that instance is waiting on.
//
// It runs on the loop goroutine from completeScope, before the scope closes
// — the last point the data is readable. An instance that produced no output
// leaves its cell unfilled, which keeps its staging slot nil exactly as a
// canceled instance's does (SRD-056.A §2.7), so a missing item is not an
// error here.
func (ls *loopState) captureInstanceOutput(
	ctx context.Context, entry *scopeEntry, path scope.DataPath,
) error {
	c := entry.capture
	if c == nil || c.item == "" {
		return nil
	}

	d, err := ls.inst.sc.plane.GetData(path, c.item)
	if err != nil {
		return nil //nolint:nilerr // an optional output, not a failure
	}

	c.value = d.Value().Get(ctx)
	c.filled = true

	return nil
}

// state reports this instance in the iteration vocabulary.
func (e *scopeExec) state() instanceState {
	a := e.awaits()

	return instanceState{
		ordinal: e.ord,
		await:   a,
		done:    a == awaitNothing && e.step.state == StepEnded,
	}
}
