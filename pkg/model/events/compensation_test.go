package events_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/generated/mockrenv"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
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

// TestCompensationThrowSurface (FR-5, SRD-059 M3): the throw-side model
// surface — CompensationWaitRef classifies a wait-for-completion throw,
// ProcessEvent consumes the loop's completion sentinel, and Exec routes a
// fire-and-forget definition to renv.Compensate (never the hub) while a
// wait definition is owned by the park path (no emission from Exec).
func TestCompensationThrowSurface(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	target := compTask(t, "target-ts", false)

	t.Run("CompensationWaitRef", func(t *testing.T) {
		cedWait, err := events.NewCompensationEventDefinition(target, true)
		require.NoError(t, err)
		it, err := events.NewIntermediateThrowEvent("t-wait", cedWait)
		require.NoError(t, err)

		ref, wait := it.CompensationWaitRef()
		require.True(t, wait)
		require.Equal(t, target.ID(), ref)

		cedFF, err := events.NewCompensationEventDefinition(nil, false)
		require.NoError(t, err)
		itFF, err := events.NewIntermediateThrowEvent("t-ff", cedFF)
		require.NoError(t, err)

		ref, wait = itFF.CompensationWaitRef()
		require.False(t, wait)
		require.Empty(t, ref)

		// a wait definition with no activityRef = scope-wide ("").
		cedScope, err := events.NewCompensationEventDefinition(nil, true)
		require.NoError(t, err)
		itScope, err := events.NewIntermediateThrowEvent("t-scope", cedScope)
		require.NoError(t, err)

		ref, wait = itScope.CompensationWaitRef()
		require.True(t, wait)
		require.Empty(t, ref)

		// a non-compensation throw is a throw with no wait ref.
		esc, err := events.NewIntermediateThrowEvent("t-esc",
			escDefForThrow(t))
		require.NoError(t, err)
		_, wait = esc.CompensationWaitRef()
		require.False(t, wait)
	})

	t.Run("ProcessEvent consumes the sentinel", func(t *testing.T) {
		ced, err := events.NewCompensationEventDefinition(nil, true)
		require.NoError(t, err)
		it, err := events.NewIntermediateThrowEvent("t-pe", ced)
		require.NoError(t, err)

		require.Error(t, it.ProcessEvent(t.Context(), nil))
		require.NoError(t, it.ProcessEvent(t.Context(), ced))
	})

	t.Run("Exec: fire-and-forget calls Compensate; wait is park-owned",
		func(t *testing.T) {
			cedFF, err := events.NewCompensationEventDefinition(target, false)
			require.NoError(t, err)
			itFF, err := events.NewIntermediateThrowEvent("x-ff", cedFF)
			require.NoError(t, err)

			mre := mockrenv.NewMockRuntimeEnvironment(t)
			mre.EXPECT().Compensate(target.ID(), false).Return()

			_, err = itFF.Exec(t.Context(), mre)
			require.NoError(t, err)

			cedW, err := events.NewCompensationEventDefinition(nil, true)
			require.NoError(t, err)
			itW, err := events.NewIntermediateThrowEvent("x-w", cedW)
			require.NoError(t, err)

			// no Compensate expectation: the wait definition is a no-op in
			// Exec — the park path owns it.
			mre2 := mockrenv.NewMockRuntimeEnvironment(t)
			_, err = itW.Exec(t.Context(), mre2)
			require.NoError(t, err)
		})
}

// escDefForThrow builds a minimal escalation definition for the non-comp
// throw case.
func escDefForThrow(t *testing.T) *events.EscalationEventDefinition {
	t.Helper()

	return events.MustEscalationEventDefinition(
		events.MustEscalation("e-ts", "E-TS",
			data.MustItemDefinition(values.NewVariable(1))))
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
