package activities

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

// TestGetParamsFailFastOnInvalidDirection is the FIX-028 §4.1 canary
// (white-box): getParams is only ever called with the data.Input/data.Output
// constants, so a Parameters error is an invariant violation — it must panic
// loudly, not surface as a parameterless task.
func TestGetParamsFailFastOnInvalidDirection(t *testing.T) {
	tsk := &task{
		activity: activity{IoSpec: &data.InputOutputSpecification{}},
	}

	require.Panics(t, func() {
		tsk.getParams(data.Direction("sideways"))
	})

	// The valid constants stay on the normal path.
	require.Empty(t, tsk.Outputs())
	require.Empty(t, tsk.Inputs())
}
