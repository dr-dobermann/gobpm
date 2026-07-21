package events_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/stretchr/testify/require"
)

// SRD-059 T-1 (events side) — CompensationEventDefinition getters and the
// Compensation boundary with its typed handler link.

// compTask builds a ServiceTask; marked selects isForCompensation.
func compTask(t *testing.T, name string, marked bool) *activities.ServiceTask {
	t.Helper()

	opts := []options.Option{activities.WithoutParams()}
	if marked {
		opts = append(opts, activities.WithCompensation())
	}

	st, err := activities.NewServiceTask(name,
		service.MustOperation(name+"-op", nil, nil, nil), opts...)
	require.NoError(t, err)

	return st
}

// TestCompensationDefinitionGetters (FR-1): Activity and WaitForCompletion
// expose the definition's activityRef and wait flag; a nil activity is legal
// (the default target context).
func TestCompensationDefinitionGetters(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	target := compTask(t, "target", false)

	ced, err := events.NewCompensationEventDefinition(target, true)
	require.NoError(t, err)
	require.Equal(t, flow.TriggerCompensation, ced.Type())
	require.Equal(t, target.ID(), ced.Activity().ID())
	require.True(t, ced.WaitForCompletion())

	// nil activityRef = the default target context, fire-and-forget.
	ced, err = events.NewCompensationEventDefinition(nil, false)
	require.NoError(t, err)
	require.Nil(t, ced.Activity())
	require.False(t, ced.WaitForCompletion())
}

// TestCompensationBoundary (FR-1/FR-2): the dedicated constructor builds an
// interrupting-flagged Compensation boundary carrying its handler link; every
// invalid parameter is rejected; the generic constructor rejects the trigger.
func TestCompensationBoundary(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ced, err := events.NewCompensationEventDefinition(nil, true)
	require.NoError(t, err)

	t.Run("valid", func(t *testing.T) {
		host := compTask(t, "host", false)
		handler := compTask(t, "undo", true)

		be, err := events.NewCompensationBoundaryEvent("c-bnd", host, ced, handler)
		require.NoError(t, err)
		require.Equal(t, host, be.AttachedTo())
		require.Equal(t, handler.ID(), be.CompensationHandler().ID())
		require.True(t, be.CancelActivity(), "the spec-default flag value")
		require.Equal(t, flow.BoundaryEventClass, be.EventClass())

		// the handler link survives a per-instance clone.
		cl, err := be.Clone()
		require.NoError(t, err)
		cbe, ok := cl.(*events.BoundaryEvent)
		require.True(t, ok)
		require.Equal(t, handler.ID(), cbe.CompensationHandler().ID())
	})

	t.Run("nil host", func(t *testing.T) {
		_, err := events.NewCompensationBoundaryEvent(
			"c-bnd", nil, ced, compTask(t, "undo-nh", true))
		require.Error(t, err)
	})

	t.Run("nil definition", func(t *testing.T) {
		_, err := events.NewCompensationBoundaryEvent(
			"c-bnd", compTask(t, "host-nd", false), nil,
			compTask(t, "undo-nd", true))
		require.Error(t, err)
	})

	t.Run("nil handler", func(t *testing.T) {
		_, err := events.NewCompensationBoundaryEvent(
			"c-bnd", compTask(t, "host-nh2", false), ced, nil)
		require.Error(t, err)
	})

	t.Run("unmarked handler", func(t *testing.T) {
		_, err := events.NewCompensationBoundaryEvent(
			"c-bnd", compTask(t, "host-um", false), ced,
			compTask(t, "undo-um", false)) // not isForCompensation
		require.Error(t, err)
	})

	t.Run("catch-event build error propagates", func(t *testing.T) {
		// options.WithName is not a valid base option for an event node — the
		// embedded catch-event build fails (the escalation-test precedent).
		_, err := events.NewCompensationBoundaryEvent(
			"c-bnd", compTask(t, "host-bo", false), ced,
			compTask(t, "undo-bo", true),
			options.WithName("not a base option"))
		require.Error(t, err)
	})

	t.Run("multiplicity: one interrupting per declaration", func(t *testing.T) {
		host := compTask(t, "host-mult", false)

		_, err := events.NewCompensationBoundaryEvent(
			"c-bnd-1", host, ced, compTask(t, "undo-m1", true))
		require.NoError(t, err)

		// the same declaration (same definition) re-attached — BoundTo rejects.
		_, err = events.NewCompensationBoundaryEvent(
			"c-bnd-2", host, ced, compTask(t, "undo-m2", true))
		require.Error(t, err)
	})

	t.Run("generic constructor rejects the trigger", func(t *testing.T) {
		_, err := events.NewBoundaryEvent(
			"c-bnd", compTask(t, "host-gen", false), ced, true)
		require.Error(t, err)
		require.ErrorContains(t, err, "NewCompensationBoundaryEvent")
	})
}
