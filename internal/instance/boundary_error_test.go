package instance

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
)

// SRD-029 M4 — the Error path. An activity raising a typed BpmnError is matched
// against its Error boundary by errorCode (FR-9); an Error End Event faults the
// instance in 0.1.0's single scope (FR-10).

// errorGuardedInstance builds start -> host(ServiceTask, op) -> normalEnd with an
// interrupting Error boundary (errorRef boundaryCode) on host -> excEnd.
func errorGuardedInstance(
	t *testing.T,
	ep eventproc.EventProducer,
	op service.Operation,
	boundaryCode string,
) (inst *Instance, normalEndID, excEndID string) {
	t.Helper()

	_ = data.CreateDefaultStates()

	p, err := process.New("srd029-m4")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	host, err := activities.NewServiceTask("host", op, activities.WithoutParams())
	require.NoError(t, err)

	normalEnd, err := events.NewEndEvent("normal-end")
	require.NoError(t, err)

	bpErr, err := bpmncommon.NewError("boundary-error", boundaryCode, nil)
	require.NoError(t, err)

	eed, err := events.NewErrorEventDefinition(bpErr)
	require.NoError(t, err)

	be, err := events.NewBoundaryEvent("err-bnd", host, eed, true)
	require.NoError(t, err)

	excEnd, err := events.NewEndEvent("exc-end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, host, normalEnd, be, excEnd} {
		require.NoError(t, p.Add(e))
	}

	_, err = flow.Link(start, host)
	require.NoError(t, err)
	_, err = flow.Link(host, normalEnd)
	require.NoError(t, err)
	_, err = flow.Link(be, excEnd)
	require.NoError(t, err)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	inst, err = New(s, scope.EmptyDataPath, enginert.Default(), ep, nil)
	require.NoError(t, err)

	return inst, normalEnd.ID(), excEnd.ID()
}

// raiseOp builds a ServiceTask operation that fails with a typed BpmnError.
func raiseOp(t *testing.T, code string) service.Operation {
	t.Helper()

	op, err := gooper.New("raise-"+code,
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition,
		) (*data.ItemDefinition, error) {
			return nil, &events.BpmnError{Code: code}
		})
	require.NoError(t, err)

	return op
}

func runToDone(t *testing.T, inst *Instance) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	require.NoError(t, inst.Run(ctx))

	select {
	case <-inst.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("instance did not finish")
	}
}

// T-9: an Error boundary whose errorRef matches the raised BpmnError routes to the
// exception flow; the instance runs on and completes, it does not fault.
func TestErrorBoundaryCatchesByCode(t *testing.T) {
	inst, normalEndID, excEndID := errorGuardedInstance(
		t, &recordingProducer{}, raiseOp(t, "E1"), "E1")

	runToDone(t, inst)

	require.Equal(t, Completed, inst.State(), "a caught error does not fault the instance")
	require.NoError(t, inst.LastErr())
	require.True(t, reachedNode(inst, excEndID), "the exception flow ran")
	require.False(t, reachedNode(inst, normalEndID),
		"the failed activity's normal path did not run")
}

// T-10: a BpmnError with no matching Error boundary falls through to the existing
// instance-fault path, carrying the code on the instance error.
func TestErrorBoundaryNoMatchFaults(t *testing.T) {
	inst, _, excEndID := errorGuardedInstance(
		t, &recordingProducer{}, raiseOp(t, "E2"), "E1") // boundary catches E1, op raises E2

	runToDone(t, inst)

	require.Equal(t, Terminated, inst.State(), "an uncaught error faults the instance")

	var be *events.BpmnError
	require.ErrorAs(t, inst.LastErr(), &be)
	require.Equal(t, "E2", be.Code)
	require.False(t, reachedNode(inst, excEndID), "no exception flow runs on a no-match")
}

// T-11: a process ending at an Error End Event faults the instance in 0.1.0's single
// scope, carrying the error's code (FR-10).
func TestErrorEndEventFaultsInstance(t *testing.T) {
	_ = data.CreateDefaultStates()

	p, err := process.New("srd029-m4-end")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	bpErr, err := bpmncommon.NewError("end-error", "E9", nil)
	require.NoError(t, err)

	eed, err := events.NewErrorEventDefinition(bpErr)
	require.NoError(t, err)

	errEnd, err := events.NewEndEvent("err-end", events.WithErrorTrigger(eed))
	require.NoError(t, err)

	for _, e := range []flow.Element{start, errEnd} {
		require.NoError(t, p.Add(e))
	}

	_, err = flow.Link(start, errEnd)
	require.NoError(t, err)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(),
		&recordingProducer{}, nil)
	require.NoError(t, err)

	runToDone(t, inst)

	require.Equal(t, Terminated, inst.State(),
		"an Error End Event ends the process in error, not normally")

	var be *events.BpmnError
	require.ErrorAs(t, inst.LastErr(), &be)
	require.Equal(t, "E9", be.Code)
}
