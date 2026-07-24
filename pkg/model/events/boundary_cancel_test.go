package events_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// TestCancelBoundaryRules: a Cancel boundary is legal only on a Transaction
// Sub-Process and is always interrupting (ADR-028 §2.4, un-defers ADR-018 §2.7).
func TestCancelBoundaryRules(t *testing.T) {
	newCancel := func() flow.EventDefinition {
		ced, err := events.NewCancelEventDefinition()
		require.NoError(t, err)

		return ced
	}

	txHost := func() flow.ActivityNode {
		tx, err := activities.NewSubProcess("tx", activities.WithTransaction())
		require.NoError(t, err)

		return tx
	}

	t.Run("an interrupting Cancel boundary attaches to a Transaction",
		func(t *testing.T) {
			_, err := events.NewBoundaryEvent("cb", txHost(), newCancel(), true)
			require.NoError(t, err)
		})

	t.Run("a non-interrupting Cancel boundary is rejected", func(t *testing.T) {
		_, err := events.NewBoundaryEvent("cb", txHost(), newCancel(), false)
		require.Error(t, err)
	})

	t.Run("a Cancel boundary on a non-Transaction is rejected",
		func(t *testing.T) {
			plain, err := activities.NewSubProcess("plain")
			require.NoError(t, err)

			_, err = events.NewBoundaryEvent("cb", plain, newCancel(), true)
			require.Error(t, err)
		})
}
