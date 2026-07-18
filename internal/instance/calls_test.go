package instance

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/exec"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/stretchr/testify/require"
)

// fakeChild is an exec.ChildProcess double: its Done channel is controlled by
// the test (pre-closed for an instant completion, closed by Terminate for the
// cascade). Failed/Outputs are scripted.
type fakeChild struct {
	id      string
	version int
	err     error
	outputs map[string]data.Data
	outErr  error
	mu      sync.Mutex
	done    chan struct{}
	closed  bool
	killed  bool
}

func newFakeChild(id string, version int) *fakeChild {
	return &fakeChild{id: id, version: version, done: make(chan struct{})}
}

func (c *fakeChild) ID() string            { return c.id }
func (c *fakeChild) Version() int          { return c.version }
func (c *fakeChild) Done() <-chan struct{} { return c.done }
func (c *fakeChild) Failed() error         { return c.err }

func (c *fakeChild) finish() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.closed {
		c.closed = true
		close(c.done)
	}
}

func (c *fakeChild) Terminate() {
	c.mu.Lock()
	c.killed = true
	c.mu.Unlock()

	c.finish()
}

func (c *fakeChild) wasKilled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.killed
}

func (c *fakeChild) Outputs(names []string) ([]data.Data, error) {
	if c.outErr != nil {
		return nil, c.outErr
	}

	out := make([]data.Data, 0, len(names))
	for _, n := range names {
		d, ok := c.outputs[n]
		if !ok {
			return nil, errs.New(errs.M("no output %q", n))
		}

		out = append(out, d)
	}

	return out, nil
}

// fakeInvoker is an exec.ProcessInvoker double: it records the call it received
// and returns a scripted child (or an error).
type fakeInvoker struct {
	child    *fakeChild
	err      error
	mu       sync.Mutex
	lastCall exec.ProcessCall
	calls    int
}

func (i *fakeInvoker) InvokeProcess(
	_ context.Context, call exec.ProcessCall,
) (exec.ChildProcess, error) {
	i.mu.Lock()
	i.lastCall = call
	i.calls++
	i.mu.Unlock()

	if i.err != nil {
		return nil, i.err
	}

	return i.child, nil
}

func (i *fakeInvoker) seen() exec.ProcessCall {
	i.mu.Lock()
	defer i.mu.Unlock()

	return i.lastCall
}

func (i *fakeInvoker) callCount() int {
	i.mu.Lock()
	defer i.mu.Unlock()

	return i.calls
}

func namedDatum(name string, v int) data.Data {
	return data.MustParameter(name,
		data.MustItemAwareElement(
			data.MustItemDefinition(values.NewVariable(v),
				foundation.WithID(name)),
			data.ReadyDataState))
}

// callProc builds start → callAct → end, with optional declared I/O and an
// optional Error boundary on the Call Activity routing to excEnd.
type callOpts struct {
	inputName    string
	outputName   string
	boundaryCode string
}

func callProc(
	t *testing.T, key string, version int, o callOpts,
) (*process.Process, *activities.CallActivity, string, string) {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	var procOpts []options.Option
	if o.inputName != "" {
		procOpts = append(procOpts, data.WithProperties(
			data.MustProperty(o.inputName,
				data.MustItemDefinition(values.NewVariable(42),
					foundation.WithID(o.inputName)),
				data.ReadyDataState)))
	}

	p, err := process.New("caller", procOpts...)
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	caOpts := []options.Option{}
	if o.inputName != "" {
		caOpts = append(caOpts, activities.WithParameters(
			data.Input, namedParam(o.inputName)))
	}
	if o.outputName != "" {
		caOpts = append(caOpts, activities.WithParameters(
			data.Output, namedParam(o.outputName)))
	}
	if version > 0 {
		caOpts = append(caOpts, activities.WithCalledVersion(version))
	}

	ca, err := activities.NewCallActivity("the-call", key, caOpts...)
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	elems := []flow.Element{start, ca, end}

	var excEndID string
	if o.boundaryCode != "" {
		bpErr, err := bpmncommon.NewError("call-error", o.boundaryCode, nil)
		require.NoError(t, err)
		eed, err := events.NewErrorEventDefinition(bpErr)
		require.NoError(t, err)
		be, err := events.NewBoundaryEvent("call-bnd", ca, eed, true)
		require.NoError(t, err)
		excEnd, err := events.NewEndEvent("exc-end")
		require.NoError(t, err)
		excEndID = excEnd.ID()
		elems = append(elems, be, excEnd)

		for _, e := range elems {
			require.NoError(t, p.Add(e))
		}
		_, err = flow.Link(start, ca)
		require.NoError(t, err)
		_, err = flow.Link(ca, end)
		require.NoError(t, err)
		_, err = flow.Link(be, excEnd)
		require.NoError(t, err)

		return p, ca, end.ID(), excEndID
	}

	for _, e := range elems {
		require.NoError(t, p.Add(e))
	}
	_, err = flow.Link(start, ca)
	require.NoError(t, err)
	_, err = flow.Link(ca, end)
	require.NoError(t, err)

	return p, ca, end.ID(), excEndID
}

func namedParam(name string) *data.Parameter {
	return data.MustParameter(name,
		data.MustItemAwareElement(
			data.MustItemDefinition(values.NewVariable(0),
				foundation.WithID(name)),
			data.ReadyDataState))
}

// runCall builds and runs an instance of p with the fake invoker, driving to a
// terminal state (or the deadline).
func runCall(t *testing.T, p *process.Process, inv exec.ProcessInvoker) *Instance {
	t.Helper()

	s, err := snapshot.New(p)
	require.NoError(t, err)

	ep := &capturingProducer{procs: map[string]eventproc.EventProcessor{}}

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(), ep, nil,
		WithInvoker(inv))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	require.NoError(t, inst.Run(ctx))

	return inst
}

func waitState(t *testing.T, inst *Instance, want State) {
	t.Helper()

	require.Eventually(t, func() bool { return inst.State() == want },
		3*time.Second, 5*time.Millisecond)
}

// TestCallParksAndLaunches (SRD-050 M3): a track reaching a Call Activity parks,
// the loop launches the child through the invoker with the resolved binding and
// linkage, and the instant-completing child resumes the caller to completion.
func TestCallParksAndLaunches(t *testing.T) {
	child := newFakeChild("child-1", 2)
	child.finish() // completes at once
	inv := &fakeInvoker{child: child}

	p, ca, _, _ := callProc(t, "callee", 2, callOpts{})
	inst := runCall(t, p, inv)

	waitState(t, inst, Completed)

	seen := inv.seen()
	require.Equal(t, "callee", seen.Key)
	require.Equal(t, 2, seen.Version)
	require.Equal(t, inst.ID(), seen.ParentInstanceID)
	require.Equal(t, ca.ID(), seen.CallNodeID)
}

// TestCallInputsClonedToChild (SRD-050 FR-6/NFR-1): the declared input is
// resolved at the caller's scope and handed to the child cloned.
func TestCallInputsClonedToChild(t *testing.T) {
	child := newFakeChild("child-2", 1)
	child.finish()
	inv := &fakeInvoker{child: child}

	p, _, _, _ := callProc(t, "callee", 0, callOpts{inputName: "seed"})
	inst := runCall(t, p, inv)

	waitState(t, inst, Completed)

	seen := inv.seen()
	require.Len(t, seen.Inputs, 1)
	require.Equal(t, "seed", seen.Inputs[0].Name())
	require.Equal(t, 42, seen.Inputs[0].Value().Get(context.Background()))
}

// TestCallCompletionBindsOutputs (SRD-050 FR-7): the child's declared output is
// read by name and committed into the caller's scope.
func TestCallCompletionBindsOutputs(t *testing.T) {
	child := newFakeChild("child-3", 1)
	child.outputs = map[string]data.Data{"result": namedDatum("result", 99)}
	child.finish()
	inv := &fakeInvoker{child: child}

	p, _, _, _ := callProc(t, "callee", 0, callOpts{outputName: "result"})
	inst := runCall(t, p, inv)

	waitState(t, inst, Completed)

	got, err := inst.DataReader().GetData("result")
	require.NoError(t, err)
	require.Equal(t, 99, got.Value().Get(context.Background()))
}

// TestCallMissingOutputFaults (SRD-050 FR-7): a declared output the child never
// produced breaks the call contract — the caller faults.
func TestCallMissingOutputFaults(t *testing.T) {
	child := newFakeChild("child-4", 1)
	child.outErr = errs.New(errs.M("no declared output"))
	child.finish()
	inv := &fakeInvoker{child: child}

	p, _, _, _ := callProc(t, "callee", 0, callOpts{outputName: "result"})
	inst := runCall(t, p, inv)

	waitState(t, inst, Terminated)
}

// TestCallChildErrorCaught (SRD-050 FR-8): a child BpmnError faults the caller
// track WITH that error; an Error boundary on the Call Activity catches it and
// the exception flow runs to completion.
func TestCallChildErrorCaught(t *testing.T) {
	child := newFakeChild("child-5", 1)
	child.err = &events.BpmnError{Code: "E_CALL"}
	child.finish()
	inv := &fakeInvoker{child: child}

	p, _, _, _ := callProc(t, "callee", 0, callOpts{boundaryCode: "E_CALL"})
	inst := runCall(t, p, inv)

	// the exception flow reaches its End → the instance completes (not faults).
	waitState(t, inst, Completed)
}

// TestCallUntypedTerminationFaults (SRD-050 FR-8): an untyped child termination
// is a technical fault — uncaught, the instance faults.
func TestCallUntypedTerminationFaults(t *testing.T) {
	child := newFakeChild("child-6", 1)
	child.err = errs.New(errs.M("child killed"))
	child.finish()
	inv := &fakeInvoker{child: child}

	p, _, _, _ := callProc(t, "callee", 0, callOpts{})
	inst := runCall(t, p, inv)

	waitState(t, inst, Terminated)
}

// TestCallNoInvokerFailsFast (SRD-050 FR-3/FR-6): with no ProcessInvoker
// configured, a Call Activity faults at once instead of parking forever.
func TestCallNoInvokerFailsFast(t *testing.T) {
	p, _, _, _ := callProc(t, "callee", 0, callOpts{})

	s, err := snapshot.New(p)
	require.NoError(t, err)

	ep := &capturingProducer{procs: map[string]eventproc.EventProcessor{}}

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(), ep, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, inst.Run(ctx))

	waitState(t, inst, Terminated)
}

// TestCallCascadeOnInstanceCancel (SRD-050 FR-9): terminating the caller while a
// call is in flight terminates the child (the cascade).
func TestCallCascadeOnInstanceCancel(t *testing.T) {
	child := newFakeChild("child-7", 1) // never finishes on its own
	inv := &fakeInvoker{child: child}

	p, _, _, _ := callProc(t, "callee", 0, callOpts{})
	inst := runCall(t, p, inv)

	// wait until the call is in flight (the invoker was called).
	require.Eventually(t, func() bool { return inv.callCount() == 1 },
		2*time.Second, 5*time.Millisecond)

	inst.Cancel()
	waitState(t, inst, Terminated)

	require.True(t, child.wasKilled(), "the child terminates with the caller")
}

// nilValueDatum builds a Ready datum whose ItemDefinition carries a nil value —
// the shape that makes the boundary-crossing clone fail (the isolation-clone
// error path).
func nilValueDatum(t *testing.T, name string) data.Data {
	t.Helper()

	id, err := data.NewItemDefinition(nil, foundation.WithID(name))
	require.NoError(t, err)

	iae, err := data.NewItemAwareElement(id, data.ReadyDataState)
	require.NoError(t, err)

	p, err := data.NewParameter(name, iae)
	require.NoError(t, err)

	return p
}

// nilIDDatum is a data.Data whose ItemDefinition is nil — the shape that makes
// the clone's ItemAwareElement construction fail.
type nilIDDatum struct{ *data.Parameter }

func (nilIDDatum) ItemDefinition() *data.ItemDefinition { return nil }

// TestCloneNamedErrors (SRD-050 NFR-1): the boundary-crossing clone rejects a
// datum it cannot copy — a nil ItemDefinition and a nil value.
func TestCloneNamedErrors(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	_, err := cloneNamed("x", nilValueDatum(t, "x"))
	require.Error(t, err, "a nil value can't be cloned")

	_, err = cloneNamed("y", nilIDDatum{namedParam("y")})
	require.Error(t, err, "a nil ItemDefinition can't be wrapped")
}

// TestCallOutputCloneFaults (SRD-050 FR-7/NFR-1): a child output the loop cannot
// clone across the boundary faults the caller.
func TestCallOutputCloneFaults(t *testing.T) {
	child := newFakeChild("child-oc", 1)
	child.outputs = map[string]data.Data{"result": nilValueDatum(t, "result")}
	child.finish()
	inv := &fakeInvoker{child: child}

	p, _, _, _ := callProc(t, "callee", 0, callOpts{outputName: "result"})
	inst := runCall(t, p, inv)

	waitState(t, inst, Terminated)
}

// TestCallLateReportDropped (SRD-050 FR-9): a completion report for a call
// already cleaned up is a benign no-op.
func TestCallLateReportDropped(t *testing.T) {
	ls := newLoopState(&Instance{})
	require.NotPanics(t, func() {
		ls.handleCallCompletion(callRequest{childID: "gone"})
	})
}

// TestCleanupCallTerminatesOwnedChild (SRD-050 FR-9): cleanupCall ends and drops
// the call owned by a track that ended without completing it.
func TestCleanupCallTerminatesOwnedChild(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ca, err := activities.NewCallActivity("the-call", "callee")
	require.NoError(t, err)

	tr := &track{}
	child := newFakeChild("owned", 1)

	ls := newLoopState(&Instance{})
	ls.calls["owned"] = &callEntry{track: tr, node: ca, child: child}

	ls.cleanupCall(tr)

	require.True(t, child.wasKilled(), "the owned child is terminated")
	require.Empty(t, ls.calls, "the entry is dropped")
}

// TestOnCallWaitingGuards (SRD-050 FR-6): the loop-side guards deliver a fault
// to the parked track's channel rather than parking it forever — a node that is
// not a Call Activity (a defensive check) and a missing ProcessInvoker.
func TestOnCallWaitingGuards(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	end, err := events.NewEndEvent("plain")
	require.NoError(t, err)

	t.Run("a non-call node faults", func(t *testing.T) {
		tr := &track{evtCh: make(chan flow.EventDefinition, 1)}
		ls := newLoopState(&Instance{invoker: &fakeInvoker{}})

		ls.onCallWaiting(t.Context(), trackEvent{track: tr, node: end})

		co, ok := (<-tr.evtCh).(*exec.CallOutcome)
		require.True(t, ok)
		require.Error(t, co.Err())
	})

	t.Run("a nil invoker faults", func(t *testing.T) {
		ca, err := activities.NewCallActivity("c", "callee")
		require.NoError(t, err)

		tr := &track{evtCh: make(chan flow.EventDefinition, 1)}
		ls := newLoopState(&Instance{}) // no invoker

		ls.onCallWaiting(t.Context(), trackEvent{track: tr, node: ca})

		co, ok := (<-tr.evtCh).(*exec.CallOutcome)
		require.True(t, ok)
		require.Error(t, co.Err())
	})
}

// TestCallInvokerErrorFaults (SRD-050 FR-6): a launch failure faults the caller
// track instead of parking it forever.
func TestCallInvokerErrorFaults(t *testing.T) {
	inv := &fakeInvoker{err: errs.New(errs.M("registry lookup failed"))}

	p, _, _, _ := callProc(t, "callee", 0, callOpts{})
	inst := runCall(t, p, inv)

	waitState(t, inst, Terminated)
}

// TestCallMissingInputFaults (SRD-050 FR-6): a declared input absent from the
// caller's scope faults the call rather than launching with a hole.
func TestCallMissingInputFaults(t *testing.T) {
	child := newFakeChild("child-x", 1)
	child.finish()
	inv := &fakeInvoker{child: child}

	// declare an input the caller's scope does not provide (no matching property).
	require.NoError(t, data.CreateDefaultStates())
	p, err := process.New("caller-noinput")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	ca, err := activities.NewCallActivity("the-call", "callee",
		activities.WithParameters(data.Input, namedParam("absent")))
	require.NoError(t, err)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, ca, end} {
		require.NoError(t, p.Add(e))
	}
	_, err = flow.Link(start, ca)
	require.NoError(t, err)
	_, err = flow.Link(ca, end)
	require.NoError(t, err)

	inst := runCall(t, p, inv)
	waitState(t, inst, Terminated)
}
