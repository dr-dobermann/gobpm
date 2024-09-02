package activities_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/stretchr/testify/require"
)

func TestNewUserTask(t *testing.T) {
	t.Run("invalid parameters",
		func(t *testing.T) {
			// empty name
			_, err := activities.NewUserTask("")
			require.Error(t, err)

			// invalid option
		})
}
