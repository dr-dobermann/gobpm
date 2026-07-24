package dtable_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/adapters/dtable"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
)

// TestConditionVocabulary covers SRD-062 T-1: every constructor over the
// supported types, with type mismatches failing loud.
func TestConditionVocabulary(t *testing.T) {
	r := rdr(map[string]any{
		"total": 150,
		"n64":   int64(7),
		"rate":  0.5,
		"tier":  "gold",
	})

	pass := func(c dtable.Condition) {
		t.Helper()

		ok, err := c(ctx, r)
		require.NoError(t, err)
		require.True(t, ok)
	}
	fail := func(c dtable.Condition) {
		t.Helper()

		ok, err := c(ctx, r)
		require.NoError(t, err)
		require.False(t, ok)
	}

	t.Run("ordered over every supported type",
		func(t *testing.T) {
			pass(dtable.GT("total", 100))
			fail(dtable.GT("total", 150))
			pass(dtable.GE("total", 150))
			pass(dtable.LT("n64", int64(8)))
			pass(dtable.LE("rate", 0.5))
			fail(dtable.LT("rate", 0.5))
			pass(dtable.GT("tier", "a"))
			pass(dtable.Between("total", 100, 150))
			fail(dtable.Between("total", 151, 200))
		})

	t.Run("equality and sets",
		func(t *testing.T) {
			pass(dtable.Eq("tier", "gold"))
			fail(dtable.Eq("tier", "silver"))
			pass(dtable.NE("tier", "silver"))
			fail(dtable.NE("tier", "gold"))
			pass(dtable.In("tier", "silver", "gold"))
			fail(dtable.In("tier", "silver", "bronze"))
		})

	t.Run("Any and Pred",
		func(t *testing.T) {
			pass(dtable.Any())
			pass(dtable.Pred(
				func(context.Context, service.DataReader) (bool, error) {
					return true, nil
				}))

			_, err := dtable.Pred(nil)(ctx, r)
			require.Error(t, err)
		})

	t.Run("type mismatch fails loud, never a silent false",
		func(t *testing.T) {
			// int datum vs float64 operand — the deployed-JSON-literal
			// class; deliberately uncoerced.
			_, err := dtable.GT("total", 100.0)(ctx, r)
			require.Error(t, err)
			require.Contains(t, err.Error(), "comparable")

			_, err = dtable.LE("tier", 5)(ctx, r)
			require.Error(t, err)
		})
}

// TestMissingInput covers SRD-062 T-2 (ADR-029 §2.5): fail loud by default,
// IfPresent opts into the DMN no-match, and non-absence failures stay loud
// even under IfPresent.
func TestMissingInput(t *testing.T) {
	r := rdr(map[string]any{"total": 150})

	t.Run("bare condition on an absent datum errors",
		func(t *testing.T) {
			_, err := dtable.GT("missing", 1)(ctx, r)
			require.Error(t, err)
			require.Contains(t, err.Error(), "missing")
			require.ErrorIs(t, err, dtable.ErrAbsent)
		})

	t.Run("equality and set reads on an absent datum error",
		func(t *testing.T) {
			_, err := dtable.Eq("missing", 1)(ctx, r)
			require.ErrorIs(t, err, dtable.ErrAbsent)

			_, err = dtable.In("missing", 1, 2)(ctx, r)
			require.ErrorIs(t, err, dtable.ErrAbsent)

			_, err = dtable.NE("missing", 1)(ctx, r)
			require.ErrorIs(t, err, dtable.ErrAbsent)
		})

	t.Run("IfPresent converts absence into no-match",
		func(t *testing.T) {
			ok, err := dtable.IfPresent(dtable.GT("missing", 1))(ctx, r)
			require.NoError(t, err)
			require.False(t, ok)
		})

	t.Run("IfPresent passes a present datum through",
		func(t *testing.T) {
			ok, err := dtable.IfPresent(dtable.GT("total", 100))(ctx, r)
			require.NoError(t, err)
			require.True(t, ok)
		})

	t.Run("IfPresent keeps non-absence failures loud",
		func(t *testing.T) {
			// present datum, mismatched operand type — not absence.
			_, err := dtable.IfPresent(dtable.GT("total", "x"))(ctx, r)
			require.Error(t, err)
		})

	t.Run("IfPresent(nil) is rejected at evaluation",
		func(t *testing.T) {
			_, err := dtable.IfPresent(nil)(ctx, r)
			require.Error(t, err)
		})

	t.Run("Any matches with nothing present",
		func(t *testing.T) {
			ok, err := dtable.Any()(ctx, rdr(nil))
			require.NoError(t, err)
			require.True(t, ok)
		})
}
