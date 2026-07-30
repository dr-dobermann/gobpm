package routers_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/adhoc"
	"github.com/dr-dobermann/gobpm/pkg/adhoc/routers"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
)

// SRD-074 FR-10 — the shipped batteries, each driving its documented order.

// fakeEval stands in for the engine's expression seam: the battery's contract
// is what it does with a result, not how the engine produced one.
type fakeEval struct {
	val data.Value
	err error
}

func (f fakeEval) Evaluate(
	context.Context, data.FormalExpression,
) (data.Value, error) {
	return f.val, f.err
}

// anyExpr builds a syntactically valid expression; the fake evaluator never
// runs it, so its body only has to exist.
func anyExpr(t *testing.T) data.FormalExpression {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	e, err := goexpr.New(nil,
		data.MustItemDefinition(values.NewVariable("")),
		func(context.Context, data.Source) (data.Value, error) {
			return values.NewVariable("a"), nil
		})
	require.NoError(t, err)

	return e
}

func requireClass(t *testing.T, err error, class string) {
	t.Helper()

	require.Error(t, err)

	var ae *errs.ApplicationError

	require.ErrorAs(t, err, &ae)
	require.True(t, ae.HasClass(class),
		"expected class %q, got %v", class, ae.Classes)
}

func TestStandardRouter(t *testing.T) {
	r := routers.Standard()
	ctx := t.Context()

	roster := []string{"a", "b", "c"}

	next, err := r.Next(ctx, adhoc.State{Activities: roster})
	require.NoError(t, err)
	require.Equal(t, roster, next,
		"nothing has run, so the whole roster is enabled — the standard's "+
			"initially-all-enabled shape")

	next, err = r.Next(ctx, adhoc.State{
		Activities: roster,
		Completed:  map[string]int{"a": 1},
		Running:    map[string]int{"b": 1},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"c"}, next,
		"an activity that ran, and one in flight, are both out of the "+
			"enabled set")

	next, err = r.Next(ctx, adhoc.State{
		Activities: roster,
		Completed:  map[string]int{"a": 1, "b": 1, "c": 2},
	})
	require.NoError(t, err)
	require.Empty(t, next, "each once, then the container ends")
}

func TestSequenceRouter(t *testing.T) {
	ctx := t.Context()

	_, err := routers.Sequence()
	requireClass(t, err, errs.EmptyNotAllowed)

	_, err = routers.Sequence("a", "")
	requireClass(t, err, errs.EmptyNotAllowed)

	r, err := routers.Sequence("c", "a", "b")
	require.NoError(t, err)

	// The step is the completion count, so the walk holds however the counts
	// are distributed across activities.
	for i, want := range []string{"c", "a", "b"} {
		next, nerr := r.Next(ctx, adhoc.State{
			Completed: map[string]int{"whatever": i},
		})
		require.NoError(t, nerr)
		require.Equal(t, []string{want}, next)
	}

	next, err := r.Next(ctx, adhoc.State{Completed: map[string]int{"x": 3}})
	require.NoError(t, err)
	require.Empty(t, next, "an exhausted list ends the container")
}

func TestExpressionRouter(t *testing.T) {
	ctx := t.Context()
	expr := anyExpr(t)

	_, err := routers.Expression(nil)
	requireClass(t, err, errs.EmptyNotAllowed)

	r, err := routers.Expression(expr)
	require.NoError(t, err)

	_, err = r.Next(ctx, adhoc.State{})
	requireClass(t, err, errs.InvalidState)

	t.Run("a list names the successors", func(t *testing.T) {
		next, nerr := r.Next(ctx, adhoc.State{
			Eval: fakeEval{val: values.NewVariable([]string{"a", "b"})},
		})
		require.NoError(t, nerr)
		require.Equal(t, []string{"a", "b"}, next)
	})

	t.Run("a lone id is wrapped", func(t *testing.T) {
		next, nerr := r.Next(ctx, adhoc.State{
			Eval: fakeEval{val: values.NewVariable("a")},
		})
		require.NoError(t, nerr)
		require.Equal(t, []string{"a"}, next)
	})

	t.Run("an empty id ends the container", func(t *testing.T) {
		next, nerr := r.Next(ctx, adhoc.State{
			Eval: fakeEval{val: values.NewVariable("")},
		})
		require.NoError(t, nerr)
		require.Empty(t, next)
	})

	t.Run("another result type is a modeling error", func(t *testing.T) {
		_, nerr := r.Next(ctx, adhoc.State{
			Eval: fakeEval{val: values.NewVariable(42)},
		})
		requireClass(t, nerr, errs.TypeCastingError)
	})

	t.Run("no value at all is reported", func(t *testing.T) {
		_, nerr := r.Next(ctx, adhoc.State{Eval: fakeEval{}})
		requireClass(t, nerr, errs.EmptyNotAllowed)
	})

	t.Run("an evaluation failure travels up", func(t *testing.T) {
		boom := errs.New(errs.M("engine is down"))

		_, nerr := r.Next(ctx, adhoc.State{Eval: fakeEval{err: boom}})
		require.ErrorIs(t, nerr, boom)
	})
}
