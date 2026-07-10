package instance

import (
	"context"

	"golang.org/x/exp/maps"

	"github.com/dr-dobermann/gobpm/pkg/errs"
)

// State represents the process instance state.
// uint32-backed so it can live in an atomic.Uint32 without a narrowing
// conversion (the instance's run state is read lock-free via State()).
type State uint32

// Instance lifecycle states — the in-memory runtime lifecycle the instance
// actually exercises (mirrors ADR-001 §4.2). The error branch and suspend are
// owned by their future ADRs, not this runtime, and are absent here.
const (
	// Created is a created instance, not yet running.
	Created State = iota
	// Active is a running instance executing its tracks.
	Active
	// Completed is an instance that finished when all tracks ended normally.
	Completed
	// Terminating is an instance canceling its tracks after ctx cancel.
	Terminating
	// Terminated is an instance that finished via cancellation.
	Terminated
)

// String returns the human-readable name of the instance state.
func (s State) String() string {
	return []string{
		"Created",
		"Active",
		"Completed",
		"Terminating",
		"Terminated",
	}[s]
}

// State returns current state of the Instance.
func (inst *Instance) State() State {
	return State(inst.state.Load())
}

// setState sets a new instance state. Written only from loop() (the single
// owner of lifecycle state) and from Run(); State() readers see it via the
// atomic, so no lock is needed.
func (inst *Instance) setState(newState State) {
	inst.state.Store(uint32(newState))
	inst.notify(ObsInstanceState, "", "", newState.String())
}

// LastErr returns the fatal error that stopped the instance (e.g. a fork
// whose target node could not be constructed), or nil. Set only by loop().
func (inst *Instance) LastErr() error {
	if e := inst.lastErr.Load(); e != nil {
		return *e
	}

	return nil
}

// Done returns a channel closed when the instance reaches a terminal state
// (Completed or Terminated). It backs host WaitCompletion (SRD-018): a closed
// channel is a non-blocking, broadcast completion signal — never dropped, unlike
// the lossy observation stream. The loop closes it on exit.
func (inst *Instance) Done() <-chan struct{} {
	return inst.loopDone
}

// Run starts the process instance execution. Execution could be stopped by
// cancel function of the context.
func (inst *Instance) Run(
	ctx context.Context,
) error {
	if ctx == nil {
		return errs.New(
			errs.M("empty context for instance"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if inst.State() != Created {
		return errs.New(
			errs.M("invalid instance state to run"),
			errs.C(errorClass, errs.InvalidState),
			errs.D("current_state", inst.State().String()))
	}

	// Derive the instance's own cancellable context so Cancel() can terminate it
	// (SRD-019). The parent ctx (the engine's, via the Thresher) still cascades —
	// canceling either drives the loop's ctx.Done() termination path.
	inst.ctx, inst.cancel = context.WithCancel(ctx)
	inst.startTime = inst.now()
	inst.setState(Active)

	// initial tracks were built by createTracks() during New; hand them to the
	// loop, which becomes the sole owner of lifecycle state from here on.
	initial := maps.Values(inst.tracks)

	go inst.loop(inst.ctx, initial)

	return nil
}

// Cancel requests termination of the instance: it cancels the instance context,
// which the loop observes (ctx.Done()) and walks Active → Terminating →
// Terminated, withdrawing its tracks. Idempotent and non-blocking — a host that
// wants to await the terminal state uses the InstanceHandle's Cancel/
// WaitCompletion. Safe before Run (no-op until the context exists).
func (inst *Instance) Cancel() {
	if inst.cancel != nil {
		inst.cancel()
	}
}

// Terminate abnormally ends the instance on behalf of a Terminate End Event
// (renv.RuntimeEnvironment, SRD-030 FR-2): it emits an evTerminate trackEvent
// onto the loop's own channel — the single-writer lane every signal uses — and
// the loop tears the instance down (stopAll). Reached only during an active run
// (a Terminate End Event is executed by the loop); idempotent — repeat events
// hit stopAll's stopping guard.
func (inst *Instance) Terminate() {
	inst.emit(trackEvent{kind: evTerminate})
}

// settleFinalState sets the instance's terminal state when loop() exits: Terminated
// if a stop/terminate drove the exit, else Completed (all tracks ended normally).
func (inst *Instance) settleFinalState(stopping bool) {
	if stopping {
		inst.setState(Terminated)

		return
	}

	inst.setState(Completed)
}
