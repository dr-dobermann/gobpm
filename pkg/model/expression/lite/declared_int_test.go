package lite_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/expression/lite"
)

// TestDeclaredIntIsSatisfiable pins the SRD-089.H M3a fix: the evaluator
// unifies every numeric to float64 (ADR-032 §2.3), so before the
// integral-coercion arm ANY declared-int expression faulted at
// evaluation — while the Multi-Instance model guard REQUIRED the
// declaration. An integral result now lands as the int the declaration
// asked for; a fractional one keeps the mismatch the check exists for.
func TestDeclaredIntIsSatisfiable(t *testing.T) {
	src := adrSource(t)

	eval := func(body string) (any, error) {
		t.Helper()

		ex, err := lite.Expr(body, data.WithResultType("int"))
		require.NoError(t, err)

		v, err := lite.New().Evaluate(ctx, ex, src)
		if err != nil {
			return nil, err
		}

		return v.Get(ctx), nil
	}

	t.Run("an integral result IS the declared int", func(t *testing.T) {
		got, err := eval("2 + 1")
		require.NoError(t, err)
		require.Equal(t, 3, got, "the value must arrive as a Go int, not "+
			"a float64 the caller has to coerce")
	})

	t.Run("a fractional result is still the mismatch", func(t *testing.T) {
		_, err := eval("7 / 2")
		require.Error(t, err)
		require.Contains(t, err.Error(), "doesn't match the declared")
	})

	t.Run("an out-of-range integral result is refused, not cast", func(t *testing.T) {
		// 1e100 passes the Trunc identity, and Go leaves an out-of-range
		// float→int conversion implementation-dependent — the bounds
		// guard keeps it a type mismatch instead of a silently wrong int.
		_, err := eval("10000000000.0 * 10000000000.0 * 10000000000.0")
		require.Error(t, err)
		require.Contains(t, err.Error(), "doesn't match the declared")
	})
}
