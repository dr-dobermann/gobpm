package activities_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockscope"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
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
	s.EXPECT().
		LoadData(task, props[0], props[1]).
		Return(nil)

	dp, err := scope.NewDataPath("/task")
	require.NoError(t, err)

	err = task.RegisterData(dp, s)
	require.NoError(t, err)

	// TODO: add associations to test
	require.NoError(t, task.LoadData(context.Background()))
	require.NoError(t, task.UploadData(context.Background(), s))
}
