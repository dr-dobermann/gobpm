package tasks

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
)

// TestOutputDatum covers FIX-026: a bad mapped-output name (worker-supplied
// policy data) fails the mapping with an error — the pre-fix Must* chain
// panicked the dispatcher instead.
func TestOutputDatum(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	t.Run("valid name builds a Ready datum",
		func(t *testing.T) {
			d, err := outputDatum("price", values.NewVariable(50))
			require.NoError(t, err)
			require.Equal(t, "price", d.Name())
		})

	t.Run("empty name fails with an error, not a panic",
		func(t *testing.T) {
			require.NotPanics(t, func() {
				_, err := outputDatum("", values.NewVariable(50))
				require.Error(t, err)
			})
		})
}
