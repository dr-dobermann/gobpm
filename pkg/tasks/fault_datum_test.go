package tasks

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
)

// TestFaultDatum covers FIX-026 §4.1: the fault-source datum builder rejects
// a nil item with a classified error — the pre-fix Must* chain panicked the
// mapper's expression evaluation instead.
func TestFaultDatum(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	t.Run("valid item builds a Ready datum",
		func(t *testing.T) {
			item := data.MustItemDefinition(values.NewVariable(404))

			d, err := faultDatum("code", item)
			require.NoError(t, err)
			require.Equal(t, "code", d.Name())
		})

	t.Run("nil item fails with an error, not a panic",
		func(t *testing.T) {
			require.NotPanics(t, func() {
				_, err := faultDatum("body", nil)
				require.Error(t, err)
				require.Contains(t, err.Error(), "body")
			})
		})
}
