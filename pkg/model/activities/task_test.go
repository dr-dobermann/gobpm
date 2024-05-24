package activities_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockscope"
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

	t.Run("TaskDataRegistration",
		func(t *testing.T) {
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
						values.NewVariable[User](User{
							Name: "Jon Doe",
							Age:  27,
						})),
					data.ReadyDataState),
			}

			task, err := activities.NewTask(
				"test",
				data.WithProperties(props...))
			require.NoError(t, err)

			scope := mockscope.NewMockScope(t)
			scope.EXPECT().
				LoadData(task, props[0], props[1]).
				Return(nil).Once()

		})
}
