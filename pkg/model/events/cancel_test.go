package events_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// TestValidateCancelEndPlacement: a Cancel End Event is legal only inside a
// Transaction (ADR-028 §2.6). A Transaction container passes; a non-Transaction
// container rejects a Cancel End but accepts ordinary ends / starts.
func TestValidateCancelEndPlacement(t *testing.T) {
	ced, err := events.NewCancelEventDefinition()
	require.NoError(t, err)
	cancelEnd, err := events.NewEndEvent("cx", events.WithCancelTrigger(ced))
	require.NoError(t, err)
	plainEnd, err := events.NewEndEvent("e")
	require.NoError(t, err)
	start, err := events.NewStartEvent("s")
	require.NoError(t, err)

	t.Run("a Transaction container allows a Cancel End", func(t *testing.T) {
		require.NoError(t,
			events.ValidateCancelEndPlacement([]flow.Node{cancelEnd}, true))
	})

	t.Run("a non-Transaction with a Cancel End is rejected", func(t *testing.T) {
		require.Error(t,
			events.ValidateCancelEndPlacement([]flow.Node{cancelEnd}, false))
	})

	t.Run("a non-Transaction without a Cancel End is fine", func(t *testing.T) {
		require.NoError(t,
			events.ValidateCancelEndPlacement([]flow.Node{plainEnd, start}, false))
	})
}
