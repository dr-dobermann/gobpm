package waiters

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

// TestPayloadDatum covers FIX-026 §4.1.3: a bad item id fails the payload
// datum build with an error — the pre-fix Must* chain panicked the hub's
// delivery goroutine instead.
func TestPayloadDatum(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	t.Run("valid id builds a Ready datum",
		func(t *testing.T) {
			d, err := payloadDatum("order_in", "payload")
			require.NoError(t, err)
			require.Equal(t, "order_in", d.Name())
		})

	t.Run("empty id fails with an error, not a panic",
		func(t *testing.T) {
			require.NotPanics(t, func() {
				_, err := payloadDatum("", "payload")
				require.Error(t, err)
			})
		})
}
