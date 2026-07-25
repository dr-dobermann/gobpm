package dtable_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/adapters/dtable"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/rules"
)

// TestRuleBuilderAndTable covers SRD-062 T-3: builder and table validation.
func TestRuleBuilderAndTable(t *testing.T) {
	ok := mustRule(t, map[string]any{"out": 1}, dtable.Any())

	t.Run("R with no conditions matches always",
		func(t *testing.T) {
			r, err := dtable.R().Then(row(map[string]any{"out": 1}))
			require.NoError(t, err)

			hit, err := r.Matches(ctx, rdr(nil))
			require.NoError(t, err)
			require.True(t, hit)
		})

	t.Run("nil condition cell rejected",
		func(t *testing.T) {
			_, err := dtable.R(dtable.Any(), nil).
				Then(row(map[string]any{"out": 1}))
			require.Error(t, err)
			require.Contains(t, err.Error(), "cell 1")

			_, err = dtable.R(nil).ThenF(
				func(context.Context, service.DataReader) (rules.Row, error) {
					return nil, nil
				})
			require.Error(t, err)
		})

	t.Run("empty outputs and nil yield rejected",
		func(t *testing.T) {
			_, err := dtable.R().Then(nil)
			require.Error(t, err)

			_, err = dtable.R().Then(rules.Row{})
			require.Error(t, err)

			_, err = dtable.R().ThenF(nil)
			require.Error(t, err)
		})

	t.Run("Then yields a copy",
		func(t *testing.T) {
			out := row(map[string]any{"out": 1})

			r, err := dtable.R().Then(out)
			require.NoError(t, err)

			out["hacked"] = values.NewVariable(2)

			got, err := r.Yield(ctx, rdr(nil))
			require.NoError(t, err)
			require.NotContains(t, got, "hacked")
		})

	t.Run("table validation",
		func(t *testing.T) {
			_, err := dtable.NewTable(" ", dtable.First, ok)
			require.Error(t, err)

			_, err = dtable.NewTable("d", dtable.HitPolicy("BOGUS"), ok)
			require.Error(t, err)
			require.Contains(t, err.Error(), "hit policy")

			_, err = dtable.NewTable("d", dtable.First)
			require.Error(t, err)

			_, err = dtable.NewTable("d", dtable.First, ok, nil)
			require.Error(t, err)
			require.Contains(t, err.Error(), "rule 1")

			tbl, err := dtable.NewTable("d", dtable.First, ok)
			require.NoError(t, err)
			require.Equal(t, "d", tbl.Name())
			require.Equal(t, dtable.First, tbl.Policy())
		})
}

// discountTable builds the shared policy fixture: two overlapping rules and
// one never-matching rule.
//
//	r0: total > 100          -> {pct: 15}
//	r1: total > 50           -> {pct: 5}
//	r2: tier == "platinum"   -> {pct: 50}
func discountTable(
	t *testing.T, policy dtable.HitPolicy, out1 map[string]any,
) *dtable.Table {
	t.Helper()

	tbl, err := dtable.NewTable("discount", policy,
		mustRule(t, map[string]any{"pct": 15}, dtable.GT("total", 100)),
		mustRule(t, out1, dtable.GT("total", 50)),
		mustRule(t, map[string]any{"pct": 50}, dtable.Eq("tier", "platinum")))
	require.NoError(t, err)

	return tbl
}

// evalOn registers tbl on a fresh engine and evaluates it over kv.
func evalOn(
	t *testing.T, tbl *dtable.Table, kv map[string]any,
) ([]rules.Row, error) {
	t.Helper()

	e, err := dtable.New()
	require.NoError(t, err)

	return e.MustRegister(tbl).Evaluate(ctx, "discount", rdr(kv))
}

// pct extracts the "pct" output of a row.
func pct(t *testing.T, r rules.Row) any {
	t.Helper()

	v, ok := r["pct"]
	require.True(t, ok)

	return v.Get(ctx)
}

// TestHitPolicies covers SRD-062 T-4 (ADR-029 §2.4).
func TestHitPolicies(t *testing.T) {
	in := map[string]any{"total": 150, "tier": "gold"}

	t.Run("Unique single match",
		func(t *testing.T) {
			rows, err := evalOn(t,
				discountTable(t, dtable.Unique, map[string]any{"pct": 5}),
				map[string]any{"total": 60, "tier": "gold"})
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.Equal(t, 5, pct(t, rows[0]))
		})

	t.Run("Unique contradiction names the ordinals",
		func(t *testing.T) {
			_, err := evalOn(t,
				discountTable(t, dtable.Unique, map[string]any{"pct": 5}), in)
			require.Error(t, err)
			require.Contains(t, err.Error(), "rules 0 and 1")
		})

	t.Run("First picks the first match and short-circuits",
		func(t *testing.T) {
			probed := false

			tbl, err := dtable.NewTable("discount", dtable.First,
				mustRule(t, map[string]any{"pct": 15}, dtable.GT("total", 100)),
				mustRule(t, map[string]any{"pct": 5}, dtable.Pred(
					func(context.Context, service.DataReader) (bool, error) {
						probed = true

						return true, nil
					})))
			require.NoError(t, err)

			rows, err := evalOn(t, tbl, in)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.Equal(t, 15, pct(t, rows[0]))
			require.False(t, probed,
				"First must not evaluate rules after the first match")
		})

	t.Run("Any agreement returns one row",
		func(t *testing.T) {
			rows, err := evalOn(t,
				discountTable(t, dtable.AnyMatch, map[string]any{"pct": 15}),
				in)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.Equal(t, 15, pct(t, rows[0]))
		})

	t.Run("Any disagreement errors",
		func(t *testing.T) {
			_, err := evalOn(t,
				discountTable(t, dtable.AnyMatch, map[string]any{"pct": 5}),
				in)
			require.Error(t, err)
			require.Contains(t, err.Error(), "disagree")
		})

	t.Run("RuleOrder and Collect return all matches in order",
		func(t *testing.T) {
			for _, p := range []dtable.HitPolicy{
				dtable.RuleOrder, dtable.Collect,
			} {
				rows, err := evalOn(t,
					discountTable(t, p, map[string]any{"pct": 5}), in)
				require.NoError(t, err)
				require.Len(t, rows, 2)
				require.Equal(t, 15, pct(t, rows[0]))
				require.Equal(t, 5, pct(t, rows[1]))
			}
		})

	t.Run("no match is an empty result",
		func(t *testing.T) {
			rows, err := evalOn(t,
				discountTable(t, dtable.Unique, map[string]any{"pct": 5}),
				map[string]any{"total": 10, "tier": "iron"})
			require.NoError(t, err)
			require.Empty(t, rows)
		})
}

// TestEngine covers the registry surface of T-4.
func TestEngine(t *testing.T) {
	tbl := discountTable(t, dtable.First, map[string]any{"pct": 5})

	t.Run("registry validation",
		func(t *testing.T) {
			e, err := dtable.New()
			require.NoError(t, err)

			require.Error(t, e.Register(nil))
			require.NoError(t, e.Register(tbl))

			err = e.Register(tbl)
			require.Error(t, err)
			require.Contains(t, err.Error(), "already registered")

			require.Panics(t, func() { e.MustRegister(tbl) })
		})

	t.Run("options apply in order and errors surface",
		func(t *testing.T) {
			_, err := dtable.New(nil)
			require.Error(t, err)

			applied := false

			_, err = dtable.New(func(*dtable.Engine) error {
				applied = true

				return nil
			})
			require.NoError(t, err)
			require.True(t, applied)

			_, err = dtable.New(func(*dtable.Engine) error {
				return errs.New(errs.M("opt boom"))
			})
			require.Error(t, err)
			require.Contains(t, err.Error(), "opt boom")
		})

	t.Run("evaluate validation",
		func(t *testing.T) {
			e, err := dtable.New()
			require.NoError(t, err)

			_, err = e.Evaluate(ctx, "", rdr(nil))
			require.Error(t, err)

			_, err = e.Evaluate(ctx, "discount", nil)
			require.Error(t, err)

			_, err = e.Evaluate(ctx, "unknown", rdr(nil))
			require.Error(t, err)
			require.Contains(t, err.Error(), "unknown")
		})

	t.Run("rule failures carry decision and ordinal",
		func(t *testing.T) {
			bad, err := dtable.NewTable("boom", dtable.First,
				mustRule(t, map[string]any{"pct": 1}, dtable.GT("absent", 1)))
			require.NoError(t, err)

			e, err := dtable.New()
			require.NoError(t, err)

			_, err = e.MustRegister(bad).Evaluate(ctx, "boom", rdr(nil))
			require.Error(t, err)
			require.Contains(t, err.Error(), "boom")
			require.Contains(t, err.Error(), "rule: 0")
		})

	t.Run("Type",
		func(t *testing.T) {
			e, err := dtable.New()
			require.NoError(t, err)
			require.Equal(t, dtable.DTableType, e.Type())
			require.Equal(t, "##DTable", dtable.DTableType)
		})
}

// the engine is a rules.Engine (the seam assert).
var _ rules.Engine = (*dtable.Engine)(nil)
