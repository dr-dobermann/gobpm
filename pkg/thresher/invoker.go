package thresher

import (
	"context"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/observability"

	"github.com/dr-dobermann/gobpm/internal/instance"
	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/exec"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/repository"
)

// InvokeProcess launches a registered process as a CHILD instance on behalf of a
// Call Activity (ADR-023 v.1 §2.7, SRD-050 FR-4). It resolves the callable
// (version 0 = latest-at-launch, else the pinned 1-based version), builds the
// child through the launchInstance path with the call's inputs seeded into its
// root scope and the call linkage stamped on its facts, runs it, and returns a
// watch handle. A missing key/version is a classified error that fails the CALL
// (the caller track faults), not the engine. Implements exec.ProcessInvoker.
func (t *Thresher) InvokeProcess(
	ctx context.Context,
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

	key, rerr := t.resolveCallable(ctx, call)
	if rerr != nil {
		return nil, rerr
	}

	// Resolve to a snapshot AND the concrete version bound (a latest-at-launch
	// call records which version it actually got). Lock-confined and released
	// before launch (the FIX-002 RC2 discipline every Start* path follows).
	s, resolved, ok := t.resolveCallLocked(key, call.Version)
	if !ok {
		return nil, errs.New(
			errs.M("InvokeProcess: no registered version for called process "+
				"%q (requested version %d)", key, call.Version),
			errs.C(errorClass, errs.ObjectNotFound),
			errs.D(observability.AttrCalledKey, key))
	}

	// The callee's contract is fixed the moment its version is (ADR-019
	// latest-at-launch), so this is where the caller's declared outputs are
	// checked against it (ADR-040 §2.4, SRD-093 FR-10); the inputs are
	// checked by the child's own construction below.
	if err := checkCallOutputs(s, call); err != nil {
		return nil, err
	}

	// NewChild only fails on a malformed snapshot or linkage; the registry
	// hands a validated snapshot and the linkage is checked above, so this is a
	// defensive wrap (the launchInstance pattern).
	settled := make(chan struct{})

	// the child receives the SAME engine-supplied option set as every
	// other launch path (SRD-082 FR-7): with a configured repository it
	// checkpoints under this engine's lease and carries the parent
	// linkage in its own record — a durable instance in its own right,
	// re-linkable after a restart.
	inst, err := instance.NewChild(s, &t.cfg, t, t.taskDist, t,
		call.Inputs, call.ParentInstanceID, call.CallNodeID,
		t.instanceOptions(settled)...)
	if err != nil {
		return nil, errs.New(errs.M("InvokeProcess: child build failed"),
			errs.C(errorClass, errs.BulidingFailed), errs.E(err))
	}

	// The child owns this context for its lifetime; cancel is retained in
	// instanceReg.stop for teardown. It must NOT be deferred — Run is
	// non-blocking (launchInstance's rationale). The engine pair is loaded
	// atomically (FIX-036 §1.1).
	ctx, cancel, err := t.instanceContext("InvokeProcess")
	if err != nil {
		return nil, err
	}
	if err = inst.Run(ctx); err != nil {
		cancel()

		return nil, errs.New(errs.M("InvokeProcess: child run failed"),
			errs.C(errorClass, errs.OperationFailed), errs.E(err))
	}

	h, displaced := t.trackInstanceLocked(inst, cancel, settled)
	stopDisplaced(displaced)
	h.reattachObservers()

	return &childProcess{inst: inst, settled: settled, version: resolved}, nil
}

// checkCallOutputs refuses a call whose caller declares an output the callee's
// contract does not (ADR-040 §2.4, SRD-093 FR-10). A contract-less callee
// declares nothing to check against and serves what its scope holds.
func checkCallOutputs(s *snapshot.Snapshot, call exec.ProcessCall) error {
	if s.IOSpec == nil {
		return nil
	}

	outputs := s.IOSpec.OutputSet()

	declared := make(map[string]bool, len(outputs))
	names := make([]string, 0, len(outputs))

	for _, out := range outputs {
		declared[out.Name()] = true
		names = append(names, out.Name())
	}

	for _, name := range call.Outputs {
		if !declared[name] {
			return errs.New(
				errs.M("call activity %q: output %q is not declared by "+
					"process %q (declared outputs: %s)", call.CallNodeID, name,
					call.Key, strings.Join(names, ", ")),
				errs.C(errorClass, errs.InvalidParameter),
				errs.D(observability.AttrCalledKey, call.Key))
		}
	}

	return nil
}

// outputsFromResult serves the caller's requested names from a contracted
// child's collected result, in the caller's order. Every requested name
// passed checkCallOutputs at launch, so a name the result lacks is a
// declared OPTIONAL output the child never produced: it simply does not
// flow (ADR-040 §2.3) — its slot is nil, and the caller binds nothing
// under it.
func outputsFromResult(result []data.Data, names []string) []data.Data {
	byName := make(map[string]data.Data, len(result))
	for _, d := range result {
		byName[d.Name()] = d
	}

	out := make([]data.Data, 0, len(names))

	for _, name := range names {
		out = append(out, byName[name])
	}

	return out
}

// childProcess is the exec.ChildProcess adapter over a launched child instance:
// a thin, read-only projection the caller loop watches. It never exposes the
// instance object, only the call protocol's surface.
type childProcess struct {
	inst *instance.Instance
	// settled is the engine-owned, cross-rebuild terminal signal: closed only
	// when the child reaches Completed/Terminated, and handed to every rebuild
	// of the same instance. The loop-exit channel is NOT terminal — it also
	// closes on a dehydration park — so the call watcher waits on this one.
	settled <-chan struct{}
	version int
}

// ID returns the child instance id.
func (c *childProcess) ID() string { return c.inst.ID() }

// Version returns the resolved 1-based version the call bound.
func (c *childProcess) Version() int { return c.version }

// Done is closed when the child reaches a terminal state. It is the settled
// signal, not the loop-exit one: a child that parks (dehydration) has not
// finished, and reporting its park as completion would resume the caller with
// a phantom success (SRD-079 §3.2's single-task contract for calls).
func (c *childProcess) Done() <-chan struct{} { return c.settled }

// Failed reports the child's terminal fault after Done: the instance's fatal
// error when it ended abnormally, else nil (a normal Completed run, or a plain
// cancellation carrying no fault).
func (c *childProcess) Failed() error {
	return c.inst.LastErr()
}

// Outputs serves the Call Activity's declared Output parameters — the call's
// return values — after completion. A contracted child serves its collected
// result, with a nil slot for a declared optional output it never produced;
// a contract-less one reads its root scope, where a missing name is a
// classified error (the call contract is broken).
func (c *childProcess) Outputs(names []string) ([]data.Data, error) {
	// A contracted child serves its collected result — the values read at
	// its completion (SRD-093 FR-9); a contract-less one serves whatever
	// its root scope holds, as it always has (ADR-040 §2.5). The result is
	// nil only for a contract-less child: a contracted one that produced
	// nothing holds an EMPTY result, and must not fall through to its raw
	// scope — that is the boundary.
	if result := c.inst.Outputs(); result != nil {
		return outputsFromResult(result, names), nil
	}

	reader := c.inst.DataReader()
	out := make([]data.Data, 0, len(names))

	for _, name := range names {
		d, err := reader.GetData(name)
		if err != nil {
			return nil, errs.New(
				errs.M("child instance %q has no declared output %q",
					c.inst.ID(), name),
				errs.C(errorClass, errs.ObjectNotFound),
				errs.D(observability.AttrChildInstanceID, c.inst.ID()),
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
// reattachChild re-finds a recorded child instance after a restart
// (SRD-082 FR-7, the instance.CallReattacher seam). Three shapes:
// resident (already recovered / never left) — a live handle; a
// TERMINAL repository record (the child finished while the engine was
// down) — an already-settled handle built from the record; an
// in-flight record — a lazy handle over the engine's settled registry,
// resolved when recovery claims the child. No record at all is loud:
// a recorded call names recorded state (ADR-033 v.4 §2.10).
func (t *Thresher) reattachChild(childID string) (exec.ChildProcess, error) {
	if inst, err := t.instanceByID(childID); err == nil {
		return &childProcess{
			inst:    inst,
			settled: t.settledFor(childID),
			version: inst.Version(),
		}, nil
	}

	if !t.cfg.repoSet {
		return nil, errs.New(
			errs.M("no repository to re-find child instance %q in", childID),
			errs.C(errorClass, errs.InvalidState))
	}

	ctx, running := t.engineContext()
	if !running {
		return nil, errs.New(
			errs.M("re-attach of %q on an engine that isn't running",
				childID),
			errs.C(errorClass, errs.InvalidState))
	}

	rec, ok, err := t.cfg.Repository().Load(ctx, childID)
	if err != nil {
		return nil, err
	}

	if !ok {
		return nil, errs.New(
			errs.M("child instance %q has no repository record — a "+
				"recorded call names recorded state", childID),
			errs.C(errorClass, errs.ObjectNotFound))
	}

	if rec.Status.IsTerminal() {
		return newSettledChild(childID, rec)
	}

	// in flight: recovery claims it in this same group sweep and closes
	// the registry channel. (A multi-engine group may split the pair —
	// the other engine's claim then resolves the child there; the
	// re-link across engines is future work, documented in the SRD.)
	return &lazyChild{thr: t, id: childID,
		settled: t.settledFor(childID)}, nil
}

// lazyChild is a re-attached, not-yet-resident child: Done comes from
// the engine's settled registry (mint-on-demand — recovery order
// between parent and child is irrelevant); the outcome and outputs
// resolve against the registry once the child settles.
type lazyChild struct {
	thr     *Thresher
	settled chan struct{}
	id      string
}

func (c *lazyChild) ID() string { return c.id }

func (c *lazyChild) Done() <-chan struct{} { return c.settled }

func (c *lazyChild) Version() int {
	if inst, err := c.thr.instanceByID(c.id); err == nil {
		return inst.Version()
	}

	return 0
}

func (c *lazyChild) Failed() error {
	if inst, err := c.thr.instanceByID(c.id); err == nil {
		return inst.LastErr()
	}

	return errs.New(
		errs.M("re-attached child %q settled but isn't tracked", c.id),
		errs.C(errorClass, errs.InvalidState))
}

func (c *lazyChild) Outputs(names []string) ([]data.Data, error) {
	inst, err := c.thr.instanceByID(c.id)
	if err != nil {
		return nil, errs.New(
			errs.M("re-attached child %q settled but isn't tracked", c.id),
			errs.C(errorClass, errs.InvalidState),
			errs.E(err))
	}

	return (&childProcess{inst: inst}).Outputs(names)
}

func (c *lazyChild) Terminate() {
	if inst, err := c.thr.instanceByID(c.id); err == nil {
		inst.Cancel()
	}
}

// settledChild is a child that finished while the engine was down: its
// terminal repository record is the whole truth — Done is already
// closed, the outcome maps from the status, the outputs decode from
// the final checkpoint's root scope.
type settledChild struct {
	outputs map[string]data.Data
	failure error
	settled chan struct{}
	id      string
	version int
}

// newSettledChild builds the handle from the terminal record.
func newSettledChild(
	id string, rec repository.InstanceRecord,
) (exec.ChildProcess, error) {
	doc, err := checkpoint.Unmarshal(rec.Payload)
	if err != nil {
		return nil, errs.New(
			errs.M("terminal child %q: its checkpoint doesn't decode", id),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err))
	}

	c := &settledChild{
		outputs: map[string]data.Data{},
		id:      id,
		version: doc.Version,
		settled: make(chan struct{}),
	}
	close(c.settled)

	if rec.Status != repository.StatusCompleted {
		c.failure = errs.New(
			errs.M("called process instance %q ended %q while the engine "+
				"was down", id, doc.Status),
			errs.C(errorClass, errs.OperationFailed))

		return c, nil
	}

	// the child's outputs live in its final root-scope data (the
	// single-segment path).
	for _, sc := range doc.Scopes {
		if strings.Count(sc.Path, "/") != 1 || len(sc.Data) == 0 {
			continue
		}

		dd, err := checkpoint.DecodeData(context.Background(), sc.Data)
		if err != nil {
			return nil, errs.New(
				errs.M("terminal child %q: its root data doesn't decode",
					id),
				errs.C(errorClass, errs.OperationFailed),
				errs.E(err))
		}

		for _, d := range dd {
			c.outputs[d.Name()] = d
		}
	}

	return c, nil
}

func (c *settledChild) ID() string { return c.id }

func (c *settledChild) Version() int { return c.version }

func (c *settledChild) Done() <-chan struct{} { return c.settled }

func (c *settledChild) Failed() error { return c.failure }

func (c *settledChild) Outputs(names []string) ([]data.Data, error) {
	out := make([]data.Data, 0, len(names))

	for _, name := range names {
		d, ok := c.outputs[name]
		if !ok {
			return nil, errs.New(
				errs.M("terminal child %q has no declared output %q",
					c.id, name),
				errs.C(errorClass, errs.ObjectNotFound))
		}

		out = append(out, d)
	}

	return out, nil
}

func (c *settledChild) Terminate() {} // already terminal

var (
	_ exec.ProcessInvoker = (*Thresher)(nil)
	_ exec.ChildProcess   = (*childProcess)(nil)
	_ exec.ChildProcess   = (*lazyChild)(nil)
	_ exec.ChildProcess   = (*settledChild)(nil)
)

// resolveCallable maps a call's reference onto the key this engine's registry
// serves, through the host's CallableResolver.
//
// It runs HOLDING NO LOCK, and that is the point: the resolver is host code,
// free to call back into this Thresher, so reaching it from inside a critical
// section is the deadlock FIX-038/FIX-041 exist to forbid. `make lock-sweep`
// checks the same rule syntactically; TestResolverMayReEnterTheEngine checks
// it dynamically.
func (t *Thresher) resolveCallable(
	ctx context.Context, call exec.ProcessCall,
) (string, error) {
	key, err := t.cfg.callableResolver.ResolveCallable(ctx,
		exec.CallableRef{Namespace: call.Namespace, Key: call.Key})
	if err != nil {
		return "", errs.New(
			errs.M("InvokeProcess: resolving called %q failed",
				callableName(call)),
			errs.C(errorClass, errs.ObjectNotFound),
			errs.D(observability.AttrCalledKey, call.Key),
			errs.E(err))
	}

	return key, nil
}

// callableName renders a call's reference for a diagnostic: the bare key when
// it is unqualified, and the namespace-qualified form otherwise, so a host
// reading the error sees the same shape its resolver was handed.
func callableName(call exec.ProcessCall) string {
	if call.Namespace == "" {
		return call.Key
	}

	return call.Namespace + "#" + call.Key
}
