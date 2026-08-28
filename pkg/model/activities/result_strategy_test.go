package activities_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
)

// keyExpr builds a per-instance key expression.
func keyExpr(t *testing.T) data.FormalExpression {
	t.Helper()

	return goexpr.Must(nil,
		data.MustItemDefinition(values.NewVariable("")),
		func(_ context.Context, _ data.Source) (data.Value, error) {
			return values.NewVariable("k"), nil
		})
}

// TestAnActivityDeclaresOneResultStrategy (SRD-090.D T-14, ADR-025 §2.6.1):
// the three are alternative READINGS of the same instances' results, not
// stages of one pipeline.
//
// An array and a map disagree about what a result is indexed by, and reduce
// says there is nothing to assemble. Composing them has no meaning to give, so
// the model is refused where it is written rather than resolved by a
// precedence rule nobody could remember.
func TestAnActivityDeclaresOneResultStrategy(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	t.Run("Multi-Instance", func(t *testing.T) {
		_, err := activities.NewMultiInstance(
			activities.WithResultMap("byKey", "result", keyExpr(t)),
			activities.WithResultReduce("total"))
		require.ErrorContains(t, err, "ONE result strategy")
		require.ErrorContains(t, err, "map", "naming what was declared")
		require.ErrorContains(t, err, "reduce", "and what would be a second")
	})

	t.Run("Standard Loop", func(t *testing.T) {
		_, err := activities.NewStandardLoop(boolExpr(t),
			activities.WithLoopResultMap("byKey", "result", keyExpr(t)),
			activities.WithLoopResultArray("results", "result"))
		require.ErrorContains(t, err, "ONE result strategy")
	})
}

// TestAResultStrategyNeedsSomewhereToPublish: a strategy with no name has
// assembled a value nobody can read.
func TestAResultStrategyNeedsSomewhereToPublish(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	_, err := activities.NewStandardLoop(boolExpr(t),
		activities.WithLoopResultArray("", "result"))
	require.ErrorContains(t, err, "needs the name it publishes under")

	_, err = activities.NewStandardLoop(boolExpr(t),
		activities.WithLoopResultReduce(""))
	require.ErrorContains(t, err, "needs the name it publishes under")
}

// TestAnAssemblingStrategyNeedsTheItemItCollects: an activity may declare more
// than one output, so which of them is assembled is not derivable — ADR-025
// §2.6 states the assembly as "that instance's outputDataItem into slot
// loopCounter of the loopDataOutputRef collection", and both halves are named.
//
// Reduce is exempt: it assembles nothing, and the name it declares IS the
// accumulating value in the enclosing scope.
func TestAnAssemblingStrategyNeedsTheItemItCollects(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	_, err := activities.NewStandardLoop(boolExpr(t),
		activities.WithLoopResultArray("results", ""))
	require.ErrorContains(t, err, "needs the per-instance item it collects")

	_, err = activities.NewMultiInstance(
		activities.WithResultMap("byKey", "", keyExpr(t)))
	require.ErrorContains(t, err, "needs the per-instance item it collects")

	sl, err := activities.NewStandardLoop(boolExpr(t),
		activities.WithLoopResultReduce("total"))
	require.NoError(t, err, "reduce assembles nothing, so it names nothing")
	require.Empty(t, sl.Result().Item())
}

// TestAMapStrategyNeedsItsKey: the key is what says which instance's result
// went where, so a map without one has no reading to offer.
func TestAMapStrategyNeedsItsKey(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	_, err := activities.NewMultiInstance(
		activities.WithResultMap("byKey", "result", nil))
	require.ErrorContains(t, err, "a nil key expression isn't allowed")

	_, err = activities.NewMultiInstance(
		activities.WithResultMap("byKey", "result", keyExpr(t), nil))
	require.ErrorContains(t, err, "a nil MapOption isn't allowed")
}

// TestADeclaredStrategyIsReadableBack: the engine asks the model what the
// instances' results mean, so what was declared has to come back out.
func TestADeclaredStrategyIsReadableBack(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	mi, err := activities.NewMultiInstance(
		activities.WithInputCollection("items", "item"))
	require.NoError(t, err)
	require.Nil(t, mi.Result(),
		"undeclared is the last-wins default, not an empty strategy")

	key := keyExpr(t)

	mi, err = activities.NewMultiInstance(
		activities.WithInputCollection("items", "item"),
		activities.WithResultMap("byOwner", "result", key,
			activities.ErrorOnKeyRewrite()))
	require.NoError(t, err)

	r := mi.Result()
	require.NotNil(t, r)
	require.Equal(t, activities.ResultMap, r.Kind())
	require.Equal(t, "byOwner", r.Name())
	require.Equal(t, "result", r.Item())
	require.Equal(t, key, r.Key())
	require.True(t, r.ErrorOnKeyRewrite())

	sl, err := activities.NewStandardLoop(boolExpr(t),
		activities.WithLoopResultArray("perPass", "result"))
	require.NoError(t, err)

	require.Equal(t, activities.ResultArray, sl.Result().Kind())
	require.Equal(t, "perPass", sl.Result().Name())
	require.False(t, sl.Result().ErrorOnKeyRewrite(),
		"the default is last-wins, and the loss is detectable rather than "+
			"silent: RUNTIME/ITERATIONS publishes the instance total")
}
