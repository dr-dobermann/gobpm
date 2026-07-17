package instance_test

import (
	"context"
	"fmt"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"log"
	"reflect"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/instance"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/stretchr/testify/require"
)

func TestInstIvalidParams(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	_, err := instance.New(nil, scope.EmptyDataPath, enginert.Default(), nil, nil)
	require.Error(t, err)

	s, err := getSnapshot("invalid_params_test")
	require.NoError(t, err)

	_, err = instance.New(s, scope.EmptyDataPath, enginert.Default(), nil, nil)
	require.Error(t, err)

	// nil engine runtime
	_, err = instance.New(s, scope.EmptyDataPath, nil, nil, nil)
	require.Error(t, err)
}

// TestNewChildValidation (SRD-050 M2): NewChild rejects the shapes that would
// leave a child instance's trace unattributable — a missing parent instance id
// or call node id — and propagates New's nil-snapshot guard. A well-formed call
// builds a rooted, linkage-stamped child.
func TestNewChildValidation(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	s, err := getSnapshot("new_child_validation")
	require.NoError(t, err)

	ep := mockeventproc.NewMockEventProducer(t)

	t.Run("empty parent instance id is rejected", func(t *testing.T) {
		_, err := instance.NewChild(s, enginert.Default(), ep, nil, nil, nil,
			"  ", "call-1")
		require.Error(t, err)
	})

	t.Run("empty call node id is rejected", func(t *testing.T) {
		_, err := instance.NewChild(s, enginert.Default(), ep, nil, nil, nil,
			"parent-1", "")
		require.Error(t, err)
	})

	t.Run("nil snapshot propagates New's guard", func(t *testing.T) {
		_, err := instance.NewChild(nil, enginert.Default(), ep, nil, nil, nil,
			"parent-1", "call-1")
		require.Error(t, err)
	})

	t.Run("a well-formed call builds the child", func(t *testing.T) {
		in := data.MustParameter("order",
			data.MustItemAwareElement(
				data.MustItemDefinition(values.NewVariable(7),
					foundation.WithID("order")),
				data.ReadyDataState))

		inst, err := instance.NewChild(s, enginert.Default(), ep, nil, nil,
			[]data.Data{in}, "parent-1", "call-1")
		require.NoError(t, err)
		require.NotNil(t, inst)

		got, err := inst.DataReader().GetData("order")
		require.NoError(t, err)
		require.Equal(t, 7, got.Value().Get(context.Background()),
			"the seeded input lands in the child's root scope")
	})
}

func TestMonitoring(t *testing.T) {
	s, err := getSnapshot("monitoring")
	require.NoError(t, err)

	ep := mockeventproc.NewMockEventProducer(t)

	inst, err := instance.New(s, scope.EmptyDataPath, enginert.Default(), ep, nil)
	require.NoError(t, err)

	// test runtime variables (served by the data plane's reserved RUNTIME
	// subtree through the instance's RuntimeVarsSupplier).
	_, err = inst.RuntimeVar("INVALID_NAME")
	require.Error(t, err)

	require.ElementsMatch(t,
		[]string{instance.StartedAt, instance.CurrState, instance.TracksCount},
		inst.RuntimeVarNames())

	ctx := context.Background()

	tc, err := inst.RuntimeVar(instance.TracksCount)
	require.NoError(t, err)
	require.Equal(t, 1, tc.Value().Get(ctx).(int))

	st, err := inst.RuntimeVar(instance.CurrState)
	require.NoError(t, err)
	require.Equal(t, instance.Created, st.Value().Get(ctx).(instance.State))

	start, err := inst.RuntimeVar(instance.StartedAt)
	require.NoError(t, err)
	require.True(t, start.Value().Get(ctx).(time.Time).IsZero())

	ctx, cancel := context.WithCancel(context.Background())

	// test instance run
	err = inst.Run(ctx)
	require.NoError(t, err)

	log.Println("instance runned")

	time.Sleep(3 * time.Second)

	cancel()
}

// TestObserveAccessors covers the SRD-018 instance-side observe accessors
// (Done + DataReader) within the instance package, so diff-coverage attributes
// them — they are otherwise exercised only cross-package, through the thresher
// InstanceHandle.
func TestObserveAccessors(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	s, err := getSnapshot("observe-accessors")
	require.NoError(t, err)

	ep := mockeventproc.NewMockEventProducer(t)

	inst, err := instance.New(s, scope.EmptyDataPath, enginert.Default(), ep, nil)
	require.NoError(t, err)

	// DataReader exposes the root data plane (runtime variables) read-only.
	r := inst.DataReader()

	st, err := r.GetData("RUNTIME/STATE")
	require.NoError(t, err)
	require.NotNil(t, st)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, inst.Run(ctx))

	// Done closes when the instance reaches a terminal state.
	select {
	case <-inst.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("instance did not signal Done")
	}

	require.Equal(t, instance.Completed, inst.State())
}

// TestInstanceCancel covers Instance.Cancel within the instance package — the
// no-op-before-Run guard and the after-Run cancel — since the public path is
// only exercised cross-package via the thresher handle (SRD-019).
func TestInstanceCancel(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	s, err := getSnapshot("cancel")
	require.NoError(t, err)

	ep := mockeventproc.NewMockEventProducer(t)

	inst, err := instance.New(s, scope.EmptyDataPath, enginert.Default(), ep, nil)
	require.NoError(t, err)

	// Cancel before Run is a no-op (no context yet).
	inst.Cancel()

	ctx, cc := context.WithCancel(context.Background())
	defer cc()
	require.NoError(t, inst.Run(ctx))

	inst.Cancel()

	select {
	case <-inst.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("Cancel did not terminate the instance")
	}

	// A terminal state either way (a fast process may complete before Cancel
	// lands); the point is Cancel is wired and the instance settles.
	require.Contains(t,
		[]instance.State{instance.Completed, instance.Terminated},
		inst.State())
}

// getSnapshot creates a simple process with user_name property
// StartEvent -> ServiceTask(print hello user_name) -> EndEvent
// and retruns its Snapshot.
func getSnapshot(pname string) (*snapshot.Snapshot, error) {
	p, err := process.New(pname,
		data.WithProperties(
			data.MustProperty(
				"user_name",
				data.MustItemDefinition(
					values.NewVariable("Dr. Dobermann"),
					foundation.WithID("user_name")),
				data.ReadyDataState)))
	if err != nil {
		return nil, err
	}

	start, err := events.NewStartEvent("start")
	if err != nil {
		return nil, err
	}

	op, err := gooper.New(
		"print user_name",
		func(ctx context.Context, _ service.DataReader, in *data.ItemDefinition) (*data.ItemDefinition, error) {
			const inId = "user_name"

			if in == nil {
				return nil,
					errs.New(
						errs.M("empty operation input"),
						errs.C(errs.EmptyNotAllowed))
			}

			if in.ID() != inId {
				return nil,
					errs.New(
						errs.M("not expected operation parameter"),
						errs.C(errs.ObjectNotFound),
						errs.D("expected_id", inId),
						errs.D("got_id", in.ID()))
			}

			userName, ok := in.Structure().Get(context.Background()).(string)
			if !ok {
				return nil,
					errs.New(
						errs.M("couldn't get user name as operation input",
							errs.D("actual_type",
								reflect.TypeOf(in.Structure()).String())))
			}

			fmt.Println("\nHello, ", userName, "!")

			return nil, nil
		},
		gooper.WithInMessage(
			bpmncommon.MustMessage(
				"user_name",
				data.MustItemDefinition(
					values.NewVariable(""),
					foundation.WithID("user_name")))),
		gooper.WithErrors(errs.ObjectNotFound, errs.EmptyNotAllowed))
	if err != nil {
		return nil, err
	}

	task, err := activities.NewServiceTask(
		"Print User Name", op, activities.WithoutParams())
	if err != nil {
		return nil, err
	}

	end, err := events.NewEndEvent("end")
	if err != nil {
		return nil, err
	}

	// register nodes
	for _, fe := range []flow.Element{start, task, end} {
		if err := p.Add(fe); err != nil {
			return nil, err
		}
	}

	// link nodes between each others
	for _, l := range []struct {
		src flow.SequenceSource
		trg flow.SequenceTarget
	}{
		{start, task},
		{task, end},
	} {
		if _, err := flow.Link(l.src, l.trg); err != nil {
			return nil, err
		}
	}

	s, err := snapshot.New(p)
	if err != nil {
		return nil, err
	}

	return s, nil
}
