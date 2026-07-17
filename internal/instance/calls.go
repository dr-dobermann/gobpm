package instance

import (
	"context"
	"strconv"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/exec"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// callActivity is the capability a Call Activity node exposes to the loop
// (SRD-050 FR-5): the call binding plus the declared I/O parameter names. The
// loop stays model-agnostic — it never imports the concrete activities package,
// only asserts this interface (the scopeHost / ExternalWorker precedent).
type callActivity interface {
	CalledKey() string
	CalledVersion() int
	CallInputs() []string
	CallOutputs() []string
}

// callRequest is the watcher goroutine's report that a launched child instance
// ended, handed to the loop so the output binding and the resume run on the
// single-writer goroutine (the jobRequest twin). It carries only the child id;
// the loop resolves the parked track and the child handle from ls.calls.
type callRequest struct {
	childID string
}

// callEntry is one loop-owned in-flight call (SRD-050 FR-6): the parked caller
// track, its Call Activity node, and the running child handle.
type callEntry struct {
	track *track
	node  flow.Node
	child exec.ChildProcess
}

// onCallWaiting launches the child instance for a parked Call Activity and
// records it so the watcher's completion report can resume the caller track — on
// the loop goroutine, unless the instance is shutting down (SRD-050 FR-6). It
// resolves the declared inputs at the caller's scope and clones them across the
// boundary (the isolation contract), then starts one watcher on the child's
// Done. A launch/resolution failure resumes the parked track with a fault so the
// instance surfaces it instead of parking forever (the onJobWaiting idiom).
func (ls *loopState) onCallWaiting(ctx context.Context, ev trackEvent) {
	if ls.stopping {
		return
	}

	ca, ok := ev.node.(callActivity)
	if !ok {
		// checkNodeType only emits evCallWaiting for a callActivity node, so this
		// is unreachable; guard defensively and fault rather than park forever.
		ev.track.evtCh <- exec.NewCallOutcome(errs.New(
			errs.M("node %q is not a call activity", ev.node.ID()),
			errs.C(errorClass, errs.InvalidObject)))

		return
	}

	if ls.inst.invoker == nil {
		ev.track.evtCh <- exec.NewCallOutcome(errs.New(
			errs.M("call activity %q: no ProcessInvoker configured — the "+
				"engine cannot launch a child process", ev.node.ID()),
			errs.C(errorClass, errs.InvalidState),
			errs.D("call_activity_node_id", ev.node.ID())))

		return
	}

	inputs, err := ls.resolveCallInputs(ev.track, ev.node, ca.CallInputs())
	if err != nil {
		ev.track.evtCh <- exec.NewCallOutcome(err)

		return
	}

	child, err := ls.inst.invoker.InvokeProcess(ctx, exec.ProcessCall{
		Key:              ca.CalledKey(),
		Version:          ca.CalledVersion(),
		Inputs:           inputs,
		ParentInstanceID: ls.inst.ID(),
		CallNodeID:       ev.node.ID(),
	})
	if err != nil {
		ev.track.evtCh <- exec.NewCallOutcome(err)

		return
	}

	// Record the call BEFORE starting the watcher: the watcher's report is
	// serviced on this same loop goroutine (callReq), strictly after this
	// returns, so a child that completes instantly cannot race the registration.
	ls.waiting[ev.track.ID()] = struct{}{}
	ls.calls[child.ID()] = &callEntry{
		track: ev.track,
		node:  ev.node,
		child: child,
	}

	ls.reportCall(observability.PhaseStarted, ev.node, child)

	go ls.inst.watchCall(ctx, child)
}

// watchCall blocks until the child instance ends, then reports it to the loop so
// the parked caller track resumes on the single-writer goroutine. Started once
// per call by onCallWaiting; it only waits and reports (NFR-2). A stopped
// instance (loopDone) drops the report.
func (inst *Instance) watchCall(ctx context.Context, child exec.ChildProcess) {
	select {
	case <-child.Done():
	case <-inst.loopDone:
		return
	case <-ctx.Done():
		return
	}

	select {
	case inst.callReq <- callRequest{childID: child.ID()}:
	case <-inst.loopDone:
	}
}

// handleCallCompletion resolves the child to its parked caller track, binds the
// child's declared outputs into the caller's scope (loop-side, single-writer),
// and resumes the track with the synthetic call-completion — or, on a child
// fault, delivers the fault so the track faults and the §2.6 chain catches it at
// the Call Activity node (SRD-050 FR-7/FR-8). Runs on the loop goroutine.
func (ls *loopState) handleCallCompletion(req callRequest) {
	entry, ok := ls.calls[req.childID]
	if !ok {
		// a late report for a call already cleaned up (its caller track ended /
		// the scope was canceled) — benign, drop it (SRD-050 FR-9).
		return
	}

	delete(ls.calls, req.childID)
	ls.flipNotParked(entry.track)

	// A child fault (a typed BpmnError, or an untyped termination) resumes the
	// caller track WITH the error: Exec returns it, the track faults, and
	// applyFailed → matchErrorBoundary catches a typed error at this node
	// (FR-8). No outputs are read on a fault.
	if err := entry.child.Failed(); err != nil {
		ls.reportCall(observability.PhaseFailed, entry.node, entry.child)
		entry.track.evtCh <- exec.NewCallOutcome(err)

		return
	}

	if err := ls.bindCallOutputs(entry); err != nil {
		// a missing declared output breaks the call contract — fault the caller
		// track through the same path (FR-7).
		ls.reportCall(observability.PhaseFailed, entry.node, entry.child)
		entry.track.evtCh <- exec.NewCallOutcome(err)

		return
	}

	ls.reportCall(observability.PhaseCompleted, entry.node, entry.child)
	entry.track.evtCh <- exec.NewCallOutcome(nil)
}

// resolveCallInputs reads the Call Activity's declared input parameters by name
// at the caller track's scope and clones each across the call boundary (SRD-050
// FR-6, NFR-1 — no live sharing). Returns the cloned data to seed the child's
// root scope. A transient frame is opened and discarded here (loop goroutine) so
// the read stays single-writer with the rest of scope access.
func (ls *loopState) resolveCallInputs(
	tr *track,
	node flow.Node,
	names []string,
) ([]data.Data, error) {
	if len(names) == 0 {
		return nil, nil
	}

	frame, err := ls.inst.sc.openFrameAt(tr.ID(), node.ID(), tr.scopePath)
	if err != nil {
		return nil, err
	}
	defer frame.Discard()

	inputs := make([]data.Data, 0, len(names))
	for _, name := range names {
		d, err := frame.GetData(name)
		if err != nil {
			return nil, errs.New(
				errs.M("call activity %q: input %q is unavailable at the "+
					"caller's scope", node.ID(), name),
				errs.C(errorClass, errs.ObjectNotFound),
				errs.E(err))
		}

		cloned, err := cloneNamed(name, d)
		if err != nil {
			return nil, err
		}

		inputs = append(inputs, cloned)
	}

	return inputs, nil
}

// bindCallOutputs reads the child's declared outputs by name and commits them
// into the caller track's scope (SRD-050 FR-7). Runs on the loop goroutine so
// the commit stays single-writer with the rest of scope access.
func (ls *loopState) bindCallOutputs(entry *callEntry) error {
	ca, _ := entry.node.(callActivity)

	names := ca.CallOutputs()
	if len(names) == 0 {
		return nil
	}

	outs, err := entry.child.Outputs(names)
	if err != nil {
		return err
	}

	// clone each output across the boundary before committing into the caller's
	// scope (NFR-1 — the child's data plane closes with it, but its values must
	// not remain shared with the caller).
	committed := make([]data.Data, 0, len(outs))
	for i, d := range outs {
		cloned, cerr := cloneNamed(names[i], d)
		if cerr != nil {
			return cerr
		}

		committed = append(committed, cloned)
	}

	_, err = ls.inst.sc.plane.Commit(entry.track.scopePath, committed...)

	return err
}

// cleanupCall ends and drops any in-flight call owned by a track that ended
// without the call completing — canceled by an interrupting boundary, a scope
// cancel, or instance terminate (SRD-050 FR-9). The child terminates with the
// caller (the engine choice); its watcher's late report then finds no entry and
// is dropped.
func (ls *loopState) cleanupCall(tr *track) {
	for id, entry := range ls.calls {
		if entry.track == tr {
			entry.child.Terminate()
			ls.reportCall(observability.PhaseTerminated, entry.node, entry.child)
			delete(ls.calls, id)
		}
	}
}

// reportCall emits a KindCall lifecycle fact (SRD-050 FR-10) carrying the called
// key, the RESOLVED version bound (the latest-at-launch audit point), and the
// child instance id.
func (ls *loopState) reportCall(
	phase observability.Phase,
	node flow.Node,
	child exec.ChildProcess,
) {
	ca, _ := node.(callActivity)

	ls.inst.report(observability.Fact{
		Kind:     observability.KindCall,
		Phase:    phase,
		NodeID:   node.ID(),
		NodeName: node.Name(),
		Details: map[string]string{
			observability.AttrCalledKey:       ca.CalledKey(),
			observability.AttrCalledVersion:   strconv.Itoa(child.Version()),
			observability.AttrChildInstanceID: child.ID(),
		},
	})
}

// cloneNamed builds a fresh, deeply-cloned datum named name from d — the
// boundary-crossing copy (SRD-050 NFR-1). The value is copied via the
// ItemAwareElement clone (structure.Clone), so the child and caller never share
// a value.
func cloneNamed(name string, d data.Data) (data.Data, error) {
	st := d.State()

	iae, err := data.NewItemAwareElement(d.ItemDefinition(), &st)
	if err != nil {
		return nil, err
	}

	cloned, err := iae.Clone()
	if err != nil {
		return nil, err
	}

	return data.NewParameter(name, cloned)
}
