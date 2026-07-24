package activities

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
)

// TestStatusParameter covers FIX-026 §4.1.1: a bad status name or value
// fails the build with an error — the pre-fix Must* chain panicked the
// track instead.
func TestStatusParameter(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	t.Run("valid inputs build a Ready parameter",
		func(t *testing.T) {
			p, err := statusParameter("status", values.NewVariable("OK"))
			require.NoError(t, err)
			require.Equal(t, "status", p.Name())
		})

	t.Run("empty name fails with an error, not a panic",
		func(t *testing.T) {
			require.NotPanics(t, func() {
				_, err := statusParameter("", values.NewVariable("OK"))
				require.Error(t, err)
			})
		})
}
