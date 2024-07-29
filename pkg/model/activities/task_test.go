package activities_test

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockscope"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestOptions(t *testing.T) {
	t.Run("no name",
		func(t *testing.T) {
			_, err := activities.NewTask("")
			require.Error(t, err)
		})

	t.Run("invalid options",
		func(t *testing.T) {
			_, err := activities.NewTask("invalid_options",
				options.WithName("error"),
				activities.WithoutParams())
			require.Error(t, err)
		})

	t.Run("normal",
		func(t *testing.T) {
			task, err := activities.NewTask("no options",
				activities.WithoutParams())
			require.NoError(t, err)

			require.False(t, task.IsMultyinstance())

			mtask, err := activities.NewTask("multyinstance",
				activities.WithMultyInstance(),
				activities.WithoutParams())
			require.NoError(t, err)

			require.True(t, mtask.IsMultyinstance())
			require.Equal(t, flow.TaskActivity, mtask.ActivityType())
		})
}

func TestTaskData(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	type User struct {
		Name string
		Age  uint
	}

	props := []*data.Property{
		data.MustProperty(
			"x",
			data.MustItemDefinition(
				values.NewVariable(42)),
			data.ReadyDataState),
		data.MustProperty(
			"user",
			data.MustItemDefinition(
				values.NewVariable(User{
					Name: "Jon Doe",
					Age:  27,
				})),
			data.ReadyDataState),
	}

	task, err := activities.NewTask(
		"test",
		data.WithProperties(props...),
		activities.WithoutParams())
	require.NoError(t, err)

	s := mockscope.NewMockScope(t)
	s.On("LoadData", task, mock.AnythingOfType("*data.Property"), mock.AnythingOfType("*data.Property")).
		Return(
			func(ndl scope.NodeDataLoader, dd ...data.Data) error {
				for _, d := range dd {
					t.Log("   >> got data: ", d.Name())

					p, ok := d.(*data.Property)
					if !ok {
						return fmt.Errorf("couldn't convert data %q to Property", d.Name())
					}

					if idx := slices.IndexFunc(
						props,
						func(prop *data.Property) bool {
							return p.Id() == prop.Id()
						}); idx == -1 {
						return fmt.Errorf("couldn't find property %q", d.Name())
					}
				}

				return nil
			})

	dp, err := scope.NewDataPath("/task")
	require.NoError(t, err)

	err = task.RegisterData(dp, s)
	require.NoError(t, err)

	// TODO: add associations to test
	require.NoError(t, task.LoadData(context.Background()))
	require.NoError(t, task.UploadData(context.Background(), s))
}
