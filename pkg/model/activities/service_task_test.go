package activities_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockrenv"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/common"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
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
				func(_ *data.ItemDefinition) (*data.ItemDefinition, error) {
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
		})
}

func TestSrvTaskExec(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	in := common.MustMessage("user name",
		data.MustItemDefinition(values.NewVariable(""),
			foundation.WithId("user_name")))

	out := common.MustMessage("hello string",
		data.MustItemDefinition(values.NewVariable(""),
			foundation.WithId("hello_str")))

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
					foundation.WithId("hello_str")),
				nil
		})
	require.NoError(t, err)

	op := service.MustOperation("hello user", in, out, hello)

	re := mockrenv.NewMockRuntimeEnvironment(t)

	re.EXPECT().
		GetDataById(scope.EmptyDataPath, "user_name").
		Return(data.MustParameter("user_name",
			data.MustItemAwareElement(
				data.MustItemDefinition(
					values.NewVariable("dr.Dobermann"),
					foundation.WithId("user_name")),
				data.ReadyDataState)),
			nil)

	// service task without output parameter
	est, err := activities.NewServiceTask("error hello",
		op, activities.WithoutParams())
	require.NoError(t, err)

	_, err = est.Exec(context.Background(), re)
	require.Error(t, err)
	t.Log("service failed as expected with error:\n", err)

	// service task with output parameter
	st, err := activities.NewServiceTask("hello", op, activities.WithoutParams())
	require.NoError(t, err)

	outs := data.MustSet("default_output")
	require.NoError(t, st.IoSpec.AddSet(outs, data.Output))

	outp := data.MustParameter("hello string",
		data.MustItemAwareElement(
			data.MustItemDefinition(
				values.NewVariable(""),
				foundation.WithId("hello_str")),
			data.UnavailableDataState))

	require.NoError(t, st.IoSpec.AddParameter(outp, data.Output))
	outs.AddParameter(outp, data.DefaultSet)

	flows, err := st.Exec(context.Background(), re)
	require.NoError(t, err)
	require.Empty(t, flows)
}
