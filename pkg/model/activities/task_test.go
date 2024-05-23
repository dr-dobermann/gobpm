package activities_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
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
	t.Run("scope.NodeDataLoader",
		func(t *testing.T) {
			mockScope := scope.NewMockScope(t)
		})
}
