package activities_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/stretchr/testify/require"
)

// mustMI builds a minimal valid sequential, collection-driven Multi-Instance.
func mustMI(t *testing.T) *activities.MultiInstanceLoopCharacteristics {
	t.Helper()

	mi, err := activities.NewMultiInstance(
		activities.WithSequential(),
		activities.WithInputCollection("items", "item"))
	require.NoError(t, err)

	return mi
}

func TestMultiInstanceBuildAndAccessors(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	t.Run("collection-driven, full", func(t *testing.T) {
		mi, err := activities.NewMultiInstance(
			activities.WithSequential(),
			activities.WithInputCollection("orders", "order"),
			activities.WithOutputCollection("results", "result"),
			activities.WithCompletionCondition(boolExpr(t)))
		require.NoError(t, err)

		require.True(t, mi.IsSequential())
		require.Nil(t, mi.LoopCardinality())
		require.Equal(t, "orders", mi.LoopDataInputRef())
		require.Equal(t, "order", mi.InputDataItem())
		require.Equal(t, "results", mi.LoopDataOutputRef())
		require.Equal(t, "result", mi.OutputDataItem())
		require.NotNil(t, mi.CompletionCondition())
	})

	t.Run("cardinality-driven defaults to parallel", func(t *testing.T) {
		mi, err := activities.NewMultiInstance(
			activities.WithCardinality(intExpr(t)))
		require.NoError(t, err)

		require.False(t, mi.IsSequential())
		require.NotNil(t, mi.LoopCardinality())
		require.Equal(t, "", mi.LoopDataInputRef())
	})
}

func TestMultiInstanceRejectsBothCardinalityAndCollection(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	mi, err := activities.NewMultiInstance(
		activities.WithCardinality(intExpr(t)),
		activities.WithInputCollection("items", "item"))
	require.Error(t, err)
	require.Nil(t, mi)
	require.Contains(t, err.Error(), "exactly one cardinality source")
}

func TestMultiInstanceRejectsNoCardinalitySource(t *testing.T) {
	mi, err := activities.NewMultiInstance(activities.WithSequential())
	require.Error(t, err)
	require.Nil(t, mi)
	require.Contains(t, err.Error(), "exactly one cardinality source")
}

func TestMultiInstanceRejectsNonIntCardinality(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	mi, err := activities.NewMultiInstance(activities.WithCardinality(boolExpr(t)))
	require.Error(t, err)
	require.Nil(t, mi)
	require.Contains(t, err.Error(), "integer")
}

func TestMultiInstanceRejectsNonBoolCompletion(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	mi, err := activities.NewMultiInstance(
		activities.WithCardinality(intExpr(t)),
		activities.WithCompletionCondition(intExpr(t)))
	require.Error(t, err)
	require.Nil(t, mi)
	require.Contains(t, err.Error(), "boolean")
}

func TestMultiInstanceRequiresInputItemForCollection(t *testing.T) {
	mi, err := activities.NewMultiInstance(
		activities.WithInputCollection("items", ""))
	require.Error(t, err)
	require.Nil(t, mi)
	require.Contains(t, err.Error(), "required")
}

// TestMultiInstanceOptionGuards covers the per-option nil/empty rejections.
func TestMultiInstanceOptionGuards(t *testing.T) {
	_, err := activities.NewMultiInstance(activities.WithCardinality(nil))
	require.ErrorContains(t, err, "nil cardinality")

	_, err = activities.NewMultiInstance(activities.WithCompletionCondition(nil))
	require.ErrorContains(t, err, "nil condition")

	_, err = activities.NewMultiInstance(activities.WithOutputCollection("out", ""))
	require.ErrorContains(t, err, "required")
}

// TestEventSubProcessRejectsMultiInstance (SRD-055 FR-3): the SRD-054 event-sub
// well-formedness guard rejects the MI marker too — it is keyed on the shared
// LoopCharacteristics interface.
func TestEventSubProcessRejectsMultiInstance(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	es, err := activities.NewSubProcess("mi-es",
		activities.WithTriggeredByEvent(),
		activities.WithLoop(mustMI(t)))
	require.NoError(t, err)

	start := sigStart(t, "es-start", true)
	task := spTask(t, "es-task")
	end, err := events.NewEndEvent("es-end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, task, end} {
		require.NoError(t, es.Add(e))
	}
	_, err = flow.Link(start, task)
	require.NoError(t, err)
	_, err = flow.Link(task, end)
	require.NoError(t, err)

	err = es.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "must not carry loop")
}
