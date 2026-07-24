package activities_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// cancelEnd builds a Cancel End Event (ADR-028 §2.6).
func cancelEnd(t *testing.T, name string) *events.EndEvent {
	t.Helper()

	ced, err := events.NewCancelEventDefinition()
	require.NoError(t, err)
	end, err := events.NewEndEvent(name, events.WithCancelTrigger(ced))
	require.NoError(t, err)

	return end
}

// txSP builds a Transaction Sub-Process with a valid inner shape carrying a
// Cancel End Event: start → task → cancel-end.
func txSP(t *testing.T, name string) *activities.SubProcess {
	t.Helper()

	sp, err := activities.NewSubProcess(name, activities.WithTransaction())
	require.NoError(t, err)

	start, err := events.NewStartEvent("s-" + name)
	require.NoError(t, err)
	task := spTask(t, "t-"+name)
	cend := cancelEnd(t, "cx-"+name)

	for _, e := range []flow.Element{start, task, cend} {
		require.NoError(t, sp.Add(e))
	}

	_, err = flow.Link(start, task)
	require.NoError(t, err)
	_, err = flow.Link(task, cend)
	require.NoError(t, err)

	return sp
}

func TestTransactionMarkerAndShapeRules(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	t.Run("WithTransaction sets IsTransaction", func(t *testing.T) {
		sp, err := activities.NewSubProcess("tx", activities.WithTransaction())
		require.NoError(t, err)
		require.True(t, sp.IsTransaction())
		require.False(t, sp.IsEventSubProcess())
	})

	t.Run("a plain sub-process is not a transaction", func(t *testing.T) {
		sp, err := activities.NewSubProcess("plain")
		require.NoError(t, err)
		require.False(t, sp.IsTransaction())
	})

	t.Run("WithTransaction and WithTriggeredByEvent are exclusive",
		func(t *testing.T) {
			_, err := activities.NewSubProcess("bad",
				activities.WithTransaction(), activities.WithTriggeredByEvent())
			require.Error(t, err)
		})

	t.Run("a Transaction clone stays a transaction", func(t *testing.T) {
		clone, err := txSP(t, "cl").Clone()
		require.NoError(t, err)
		require.True(t, clone.(*activities.SubProcess).IsTransaction())
	})

	t.Run("a Cancel End inside a Transaction validates", func(t *testing.T) {
		require.NoError(t, txSP(t, "ok").Validate())
	})

	t.Run("a Cancel End in a plain sub-process is rejected", func(t *testing.T) {
		sp, err := activities.NewSubProcess("plainCancel")
		require.NoError(t, err)
		start, err := events.NewStartEvent("s")
		require.NoError(t, err)
		cend := cancelEnd(t, "cx")

		for _, e := range []flow.Element{start, cend} {
			require.NoError(t, sp.Add(e))
		}
		_, err = flow.Link(start, cend)
		require.NoError(t, err)

		require.Error(t, sp.Validate())
	})

	t.Run("a nested Transaction is rejected", func(t *testing.T) {
		outer, err := activities.NewSubProcess("outer", activities.WithTransaction())
		require.NoError(t, err)
		inner := txSP(t, "inner")
		start, err := events.NewStartEvent("os")
		require.NoError(t, err)
		end, err := events.NewEndEvent("oe")
		require.NoError(t, err)

		for _, e := range []flow.Element{start, inner, end} {
			require.NoError(t, outer.Add(e))
		}
		_, err = flow.Link(start, inner)
		require.NoError(t, err)
		_, err = flow.Link(inner, end)
		require.NoError(t, err)

		require.Error(t, outer.Validate())
	})
}
