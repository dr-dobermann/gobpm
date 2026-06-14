package instance

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/stretchr/testify/require"
)

// newResultTask builds a ServiceTask whose operation produces an item with
// the given id and value through the per-execution frame (SRD-007 FR-5).
func newResultTask(
	t *testing.T,
	name, itemID string,
	val any,
	ran *atomic.Int32,
	fail bool,
) *activities.ServiceTask {
	t.Helper()

	out := bpmncommon.MustMessage(name+" result",
		data.MustItemDefinition(values.NewVariable(""),
			foundation.WithID(itemID)))

	op, err := gooper.New(
		name+" op",
		func(_ context.Context, _ service.DataReader, _ *data.ItemDefinition) (*data.ItemDefinition, error) {
			if fail {
				return nil, fmt.Errorf("functor failed by design")
			}

			ran.Add(1)

			return data.MustItemDefinition(
					values.NewVariable(val),
					foundation.WithID(itemID)),
				nil
		},
		gooper.WithOutMessage(out))
	require.NoError(t, err)

	st, err := activities.NewServiceTask(
		name,
		op,
		activities.WithoutParams())
	require.NoError(t, err)

	return st
}

// TestDataCrossingParallelFork is SRD-007 V3: two tracks fork on a Parallel
// gateway and each executes its OWN node producing data through its own
// execution frame; both results land in the container scope as atomic
// commits, and the engine is -race clean (the suite runs under -race).
//
//	start ─> parallelGW ─┬─> taskA ─> endA
//	                     └─> taskB ─> endB
func TestDataCrossingParallelFork(t *testing.T) {
	_ = data.CreateDefaultStates()

	p, err := process.New("data-fork")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	pg, err := gateways.NewParallelGateway(
		gateways.WithDirection(gateways.Diverging))
	require.NoError(t, err)

	var ran atomic.Int32

	taskA := newResultTask(t, "taskA", "res-a", "from-A", &ran, false)
	taskB := newResultTask(t, "taskB", "res-b", "from-B", &ran, false)

	endA, err := events.NewEndEvent("endA")
	require.NoError(t, err)
	endB, err := events.NewEndEvent("endB")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, pg, taskA, taskB, endA, endB} {
		require.NoError(t, p.Add(e))
	}

	_, err = flow.Link(start, pg)
	require.NoError(t, err)
	_, err = flow.Link(pg, taskA)
	require.NoError(t, err)
	_, err = flow.Link(pg, taskB)
	require.NoError(t, err)
	_, err = flow.Link(taskA, endA)
	require.NoError(t, err)
	_, err = flow.Link(taskB, endB)
	require.NoError(t, err)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(),
		mockeventproc.NewMockEventProducer(t), nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, inst.Run(ctx))

	require.Eventually(t,
		func() bool { return inst.State() == Completed },
		2*time.Second, 5*time.Millisecond)
	require.NoError(t, inst.LastErr())
	require.EqualValues(t, 2, ran.Load(), "both branch tasks must execute")

	// both branches' results were committed to the container scope.
	for id, want := range map[string]string{
		"res-a": "from-A",
		"res-b": "from-B",
	} {
		d, err := inst.dataPlane.GetDataByID(inst.rootScope, id)
		require.NoError(t, err, "result %q must be committed", id)
		require.Equal(t, want, d.Value().Get(ctx))
	}
}

// TestFrameDiscardOnFailure is SRD-007 V4: a failing node execution leaves
// ZERO trace in the container scope — the frame is discarded uncommitted
// (ADR-010 §2.3).
func TestFrameDiscardOnFailure(t *testing.T) {
	_ = data.CreateDefaultStates()

	p, err := process.New("data-fail")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	var ran atomic.Int32

	bad := newResultTask(t, "bad", "res-bad", "never", &ran, true)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, bad, end} {
		require.NoError(t, p.Add(e))
	}

	_, err = flow.Link(start, bad)
	require.NoError(t, err)
	_, err = flow.Link(bad, end)
	require.NoError(t, err)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(),
		mockeventproc.NewMockEventProducer(t), nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, inst.Run(ctx))

	require.Eventually(t,
		func() bool {
			snap := inst.tracksSnap.Load()
			if snap == nil {
				return false
			}

			for _, tr := range *snap {
				if tr.inState(TrackFailed) {
					return true
				}
			}

			return false
		},
		2*time.Second, 5*time.Millisecond,
		"the failing task must fail its track")

	// nothing of the failed execution reached the container scope.
	_, err = inst.dataPlane.GetDataByID(inst.rootScope, "res-bad")
	require.Error(t, err, "a discarded frame must leave no scope residue")
}
