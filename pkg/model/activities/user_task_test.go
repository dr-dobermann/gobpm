package activities_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/hinteraction/consinp"
	"github.com/stretchr/testify/require"
)

func TestNewUserTask(t *testing.T) {
	t.Run("invalid parameters",
		func(t *testing.T) {
			// empty name
			_, err := activities.NewUserTask("")
			require.Error(t, err)

			// empty renderer
			_, err = activities.NewUserTask("invalid",
				activities.WithRenderer(nil))
			require.Error(t, err)

			// duplicate renderers
			r, err := consinp.NewRenderer(
				consinp.WithMessager("Hello world", "Hello world"))
			require.NoError(t, err)
			_, err = activities.NewUserTask("invalid",
				activities.WithRenderer(r),
				activities.WithRenderer(r))
			require.Error(t, err)
		})

	t.Run("normal",
		func(t *testing.T) {
		})
}
