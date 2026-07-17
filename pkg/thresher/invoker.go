package thresher

import (
	"context"

	"github.com/dr-dobermann/gobpm/internal/instance"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/exec"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

// InvokeProcess launches a registered process as a CHILD instance on behalf of a
// Call Activity (ADR-023 v.1 §2.7, SRD-050 FR-4). It resolves the callable
// (version 0 = latest-at-launch, else the pinned 1-based version), builds the
// child through the launchInstance path with the call's inputs seeded into its
// root scope and the call linkage stamped on its facts, runs it, and returns a
// watch handle. A missing key/version is a classified error that fails the CALL
// (the caller track faults), not the engine. Implements exec.ProcessInvoker.
func (t *Thresher) InvokeProcess(
	_ context.Context,
	call exec.ProcessCall,
) (exec.ChildProcess, error) {
	if call.Key == "" {
		return nil, errs.New(
			errs.M("InvokeProcess: empty called-process key isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if call.Version < 0 {
		return nil, errs.New(
			errs.M("InvokeProcess: version must be >= 0 (0 = latest), got %d",
				call.Version),
			errs.C(errorClass, errs.InvalidParameter))
	}

	if call.ParentInstanceID == "" || call.CallNodeID == "" {
		return nil, errs.New(
			errs.M("InvokeProcess: parent instance id and call node id are "+
				"required for the call linkage"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if err := t.ensureStarted(); err != nil {
		return nil, err
	}

	// Resolve to a snapshot AND the concrete version bound (a latest-at-launch
	// call records which version it actually got). Lock-confined and released
	// before launch (the FIX-002 RC2 discipline every Start* path follows).
	s, resolved, ok := t.resolveCallLocked(call.Key, call.Version)
	if !ok {
		return nil, errs.New(
			errs.M("InvokeProcess: no registered version for called process "+
				"%q (requested version %d)", call.Key, call.Version),
			errs.C(errorClass, errs.ObjectNotFound),
			errs.D("called_key", call.Key))
	}

	// NewChild only fails on a malformed snapshot or linkage; the registry
	// hands a validated snapshot and the linkage is checked above, so this is a
	// defensive wrap (the launchInstance pattern).
	inst, err := instance.NewChild(s, &t.cfg, t, t.taskDist, t,
		call.Inputs, call.ParentInstanceID, call.CallNodeID)
	if err != nil {
		return nil, errs.New(errs.M("InvokeProcess: child build failed"),
			errs.C(errorClass, errs.BulidingFailed), errs.E(err))
	}

	// The child owns this context for its lifetime; cancel is retained in
	// instanceReg.stop for teardown. It must NOT be deferred — Run is
	// non-blocking (launchInstance's rationale).
	ctx, cancel := context.WithCancel(t.ctx)
	if err = inst.Run(ctx); err != nil {
		cancel()

		return nil, errs.New(errs.M("InvokeProcess: child run failed"),
			errs.C(errorClass, errs.OperationFailed), errs.E(err))
	}

	t.trackInstanceLocked(inst, cancel)

	return &childProcess{inst: inst, version: resolved}, nil
}

// childProcess is the exec.ChildProcess adapter over a launched child instance:
// a thin, read-only projection the caller loop watches. It never exposes the
// instance object, only the call protocol's surface.
type childProcess struct {
	inst    *instance.Instance
	version int
}

// ID returns the child instance id.
func (c *childProcess) ID() string { return c.inst.ID() }

// Version returns the resolved 1-based version the call bound.
func (c *childProcess) Version() int { return c.version }

// Done is closed when the child reaches a terminal state (the instance's
// never-dropped terminal signal).
func (c *childProcess) Done() <-chan struct{} { return c.inst.Done() }

// Failed reports the child's terminal fault after Done: the instance's fatal
// error when it ended abnormally, else nil (a normal Completed run, or a plain
// cancellation carrying no fault).
func (c *childProcess) Failed() error {
	return c.inst.LastErr()
}

// Outputs reads the named data from the child's root scope after completion —
// the Call Activity's declared Output parameters, the call's return values. A
// missing name is a classified error (the call contract is broken).
func (c *childProcess) Outputs(names []string) ([]data.Data, error) {
	reader := c.inst.DataReader()
	out := make([]data.Data, 0, len(names))

	for _, name := range names {
		d, err := reader.GetData(name)
		if err != nil {
			return nil, errs.New(
				errs.M("child instance %q has no declared output %q",
					c.inst.ID(), name),
				errs.C(errorClass, errs.ObjectNotFound),
				errs.D("child_instance_id", c.inst.ID()),
				errs.E(err))
		}

		out = append(out, d)
	}

	return out, nil
}

// Terminate ends the child (the cancel cascade, ADR-023 §2.7). Idempotent — the
// instance's Cancel is idempotent.
func (c *childProcess) Terminate() { c.inst.Cancel() }

// Interface implementation checks.
var (
	_ exec.ProcessInvoker = (*Thresher)(nil)
	_ exec.ChildProcess   = (*childProcess)(nil)
)
