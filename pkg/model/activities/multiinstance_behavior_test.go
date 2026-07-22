package activities_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// sigDef builds a signal event definition to use as a behavior event ref.
func sigDef(t *testing.T, name string) flow.EventDefinition {
	t.Helper()

	sig, err := events.NewSignal(name, nil)
	require.NoError(t, err)
	def, err := events.NewSignalEventDefinition(sig)
	require.NoError(t, err)

	return def
}

// mustImplicitThrow builds an ImplicitThrowEvent over a signal definition.
func mustImplicitThrow(t *testing.T) *events.ImplicitThrowEvent {
	t.Helper()

	ite, err := events.NewImplicitThrowEvent("beh", sigDef(t, "c"))
	require.NoError(t, err)

	return ite
}

// baseMI builds a minimal valid sequential collection MI plus the given options.
func baseMI(
	t *testing.T, opts ...activities.MultiInstanceOption,
) (*activities.MultiInstanceLoopCharacteristics, error) {
	t.Helper()

	return activities.NewMultiInstance(append([]activities.MultiInstanceOption{
		activities.WithSequential(),
		activities.WithInputCollection("items", "item"),
	}, opts...)...)
}

// TestMultiInstanceBehaviorValidation: behavior defaults to All, and each mode
// requires exactly its own event source (ADR-025 §2.8).
func TestMultiInstanceBehaviorValidation(t *testing.T) {
	t.Run("default is All, no event", func(t *testing.T) {
		mi, err := baseMI(t)
		require.NoError(t, err)
		require.Equal(t, activities.BehaviorAll, mi.Behavior())
	})

	t.Run("None requires the none event", func(t *testing.T) {
		_, err := baseMI(t, activities.WithBehavior(activities.BehaviorNone))
		require.Error(t, err)

		mi, err := baseMI(t, activities.WithBehavior(activities.BehaviorNone),
			activities.WithNoneBehaviorEvent(sigDef(t, "n")))
		require.NoError(t, err)
		require.NotNil(t, mi.NoneBehaviorEvent())
	})

	t.Run("One requires the one event", func(t *testing.T) {
		_, err := baseMI(t, activities.WithBehavior(activities.BehaviorOne))
		require.Error(t, err)

		mi, err := baseMI(t, activities.WithBehavior(activities.BehaviorOne),
			activities.WithOneBehaviorEvent(sigDef(t, "o")))
		require.NoError(t, err)
		require.NotNil(t, mi.OneBehaviorEvent())
	})

	t.Run("Complex requires a definition", func(t *testing.T) {
		_, err := baseMI(t, activities.WithBehavior(activities.BehaviorComplex))
		require.Error(t, err)

		cbd, err := activities.NewComplexBehaviorDefinition(
			boolExpr(t), mustImplicitThrow(t))
		require.NoError(t, err)

		mi, err := baseMI(t, activities.WithBehavior(activities.BehaviorComplex),
			activities.WithComplexBehavior(cbd))
		require.NoError(t, err)
		require.Len(t, mi.ComplexBehavior(), 1)
	})

	t.Run("All forbids a behavior event", func(t *testing.T) {
		_, err := baseMI(t, activities.WithNoneBehaviorEvent(sigDef(t, "n")))
		require.Error(t, err)
	})

	t.Run("mismatched ref for the mode is rejected", func(t *testing.T) {
		_, err := baseMI(t, activities.WithBehavior(activities.BehaviorNone),
			activities.WithOneBehaviorEvent(sigDef(t, "o")))
		require.Error(t, err)
	})

	t.Run("unknown behavior is rejected", func(t *testing.T) {
		_, err := baseMI(t, activities.WithBehavior("bogus"))
		require.Error(t, err)
	})

	t.Run("a nil behavior event is rejected", func(t *testing.T) {
		_, err := baseMI(t, activities.WithNoneBehaviorEvent(nil))
		require.Error(t, err)

		_, err = baseMI(t, activities.WithOneBehaviorEvent(nil))
		require.Error(t, err)

		_, err = baseMI(t, activities.WithComplexBehavior(nil))
		require.Error(t, err)
	})
}

// TestComplexBehaviorDefinition: the constructor requires a boolean condition
// and a non-nil event.
func TestComplexBehaviorDefinition(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		cbd, err := activities.NewComplexBehaviorDefinition(
			boolExpr(t), mustImplicitThrow(t))
		require.NoError(t, err)
		require.NotNil(t, cbd.Condition())
		require.NotNil(t, cbd.Event())
	})

	t.Run("nil condition rejected", func(t *testing.T) {
		_, err := activities.NewComplexBehaviorDefinition(nil, mustImplicitThrow(t))
		require.Error(t, err)
	})

	t.Run("nil event rejected", func(t *testing.T) {
		_, err := activities.NewComplexBehaviorDefinition(boolExpr(t), nil)
		require.Error(t, err)
	})

	t.Run("non-boolean condition rejected", func(t *testing.T) {
		_, err := activities.NewComplexBehaviorDefinition(
			intExpr(t), mustImplicitThrow(t))
		require.Error(t, err)
	})
}
