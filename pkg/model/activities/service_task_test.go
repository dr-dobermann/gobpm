package activities_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockrenv"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestServiceTaskDefinition(t *testing.T) {
	op, err := service.NewOperation("my op", nil, nil, nil)
	require.NoError(t, err)

	t.Run("empty args",
		func(t *testing.T) {
			st, err := activities.NewServiceTask("", nil,
				activities.WithoutParams())
			require.Error(t, err)
			require.Empty(t, st)

			st, err = activities.NewServiceTask("test", nil,
				activities.WithoutParams())
			require.Error(t, err)
			require.Empty(t, st)
		})

	t.Run("multyinsatance",
		func(t *testing.T) {
			st, err := activities.NewServiceTask("test", op,
				activities.WithMultyInstance(),
				activities.WithoutParams())
			require.NoError(t, err)
			require.Equal(t, "test", st.Name())
			require.Equal(t, true, st.IsMultyinstance())
			require.Equal(t, "##unspecified", st.Implementation())
		})

	t.Run("invalid options check",
		func(t *testing.T) {
			_, err := activities.NewServiceTask(
				"test",
				op,
				events.WithParallel(),
			)
			require.Error(t, err)
		})

	t.Run("empty operation",
		func(t *testing.T) {
			st, err := activities.NewServiceTask("test",
				op,
				activities.WithoutParams())
			require.NoError(t, err)

			re := mockrenv.NewMockRuntimeEnvironment(t)

			_, err = st.Exec(context.Background(), re)
			t.Log(err)
			require.Error(t, err)
		})

	t.Run("simple no args operation",
		func(t *testing.T) {
			hello, err := gooper.New(
				func(_ context.Context, _ *data.ItemDefinition) (*data.ItemDefinition, error) {
					fmt.Println("  >>>> Hello, world!")

					return nil, nil
				})
			require.NoError(t, err)
			sop := service.MustOperation("hello world", nil, nil, hello)

			st, err := activities.NewServiceTask("hello",
				sop, activities.WithoutParams())
			require.NoError(t, err)

			_, err = st.Exec(context.Background(), nil)
			require.Error(t, err)

			re := mockrenv.NewMockRuntimeEnvironment(t)

			flows, err := st.Exec(context.Background(), re)
			require.NoError(t, err)
			require.Empty(t, flows)

			require.Equal(t, flow.ServiceTask, st.TaskType())
			require.Equal(t, st, st.Node())
		})
}

func TestSrvTaskExec(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	in := bpmncommon.MustMessage("user name",
		data.MustItemDefinition(values.NewVariable(""),
			foundation.WithID("user_name")))

	out := bpmncommon.MustMessage("hello string",
		data.MustItemDefinition(values.NewVariable(""),
			foundation.WithID("hello_str")))

	hello, err := gooper.New(
		func(ctx context.Context, d *data.ItemDefinition) (*data.ItemDefinition, error) {
			v := d.Structure().Get(context.Background())
			name, ok := v.(string)
			if !ok {
				return nil,
					fmt.Errorf("expecting string but got %s",
						reflect.TypeOf(v).String())
			}

			hello_str := "  >>>> Hello, " + name

			fmt.Println(hello_str)

			return data.MustItemDefinition(
					values.NewVariable(hello_str),
					foundation.WithID("hello_str")),
				nil
		})
	require.NoError(t, err)

	op := service.MustOperation("hello user", in, out, hello)

	// the input data is missing from the execution's resolution → Exec fails.
	bad := mockrenv.NewMockRuntimeEnvironment(t)
	bad.EXPECT().
		GetDataByID("user_name").
		Return(nil, fmt.Errorf("not found"))

	est, err := activities.NewServiceTask("error hello",
		op, activities.WithoutParams())
	require.NoError(t, err)

	_, err = est.Exec(context.Background(), bad)
	require.Error(t, err)
	t.Log("service failed as expected with error:\n", err)

	// happy path: the input resolves, the operation runs on its
	// per-execution clone, and the result reaches the frame as a put.
	re := mockrenv.NewMockRuntimeEnvironment(t)

	re.EXPECT().
		GetDataByID("user_name").
		Return(data.MustParameter("user_name",
			data.MustItemAwareElement(
				data.MustItemDefinition(
					values.NewVariable("dr.Dobermann"),
					foundation.WithID("user_name")),
				data.ReadyDataState)),
			nil)

	var put data.Data

	re.EXPECT().
		Put(mock.Anything).
		RunAndReturn(func(dd ...data.Data) error {
			require.Len(t, dd, 1)
			put = dd[0]

			return nil
		})

	st, err := activities.NewServiceTask("hello", op, activities.WithoutParams())
	require.NoError(t, err)

	flows, err := st.Exec(context.Background(), re)
	require.NoError(t, err)
	require.Empty(t, flows)

	// the operation result is handed to the frame, keyed by the outgoing
	// message item id; the producer stage copies it into the output instance.
	require.NotNil(t, put)
	require.Equal(t, "hello_str", put.ItemDefinition().ID())
	require.Equal(t, "  >>>> Hello, dr.Dobermann",
		put.Value().Get(context.Background()))

	// the NODE's operation message stays untouched — the run mutated the
	// per-execution clone only (ADR-010 §2.3).
	require.Equal(t, "",
		out.Item().Structure().Get(context.Background()))
}
