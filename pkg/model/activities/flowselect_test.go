package activities

import (
	"context"
	"errors"
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockrenv"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	exprengine "github.com/dr-dobermann/gobpm/pkg/model/expression/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/stretchr/testify/require"
)

// selectRe builds a RuntimeEnvironment mock serving the Go expression engine.
func selectRe(t *testing.T) *mockrenv.MockRuntimeEnvironment {
	t.Helper()

	re := mockrenv.NewMockRuntimeEnvironment(t)
	re.EXPECT().ExpressionEngine().Return(exprengine.New()).Maybe()

	return re
}

// constCond builds a bool-typed condition evaluating to v.
func constCond(t *testing.T, v bool) data.FormalExpression {
	t.Helper()

	c, err := goexpr.New(nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(_ context.Context, _ data.Source) (data.Value, error) {
			return values.NewVariable(v), nil
		})
	require.NoError(t, err)

	return c
}

// failCond builds a bool-typed condition whose evaluation fails.
func failCond(t *testing.T) data.FormalExpression {
	t.Helper()

	c, err := goexpr.New(nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			_, err := ds.Find(ctx, "missing")

			return nil, err
		})
	require.NoError(t, err)

	return c
}

// intCond builds a NON-bool condition (the type-rejection case).
func intCond(t *testing.T) data.FormalExpression {
	t.Helper()

	c, err := goexpr.New(nil,
		data.MustItemDefinition(values.NewVariable(0)),
		func(_ context.Context, _ data.Source) (data.Value, error) {
			return values.NewVariable(1), nil
		})
	require.NoError(t, err)

	return c
}

// linked builds a source activity with one outgoing flow per option applied
// in order, returning the source and its flows.
func linked(
	t *testing.T, conds ...data.FormalExpression,
) (*activity, []*flow.SequenceFlow) {
	t.Helper()

	src, err := newActivity("src", WithoutParams())
	require.NoError(t, err)

	flows := make([]*flow.SequenceFlow, 0, len(conds))

	for i, c := range conds {
		trg, err := newActivity("trg", WithoutParams())
		require.NoError(t, err)

		opts := []options.Option{}
		if c != nil {
			opts = append(opts, flow.WithCondition(c))
		}

		sf, err := flow.Link(src, trg, opts...)
		require.NoError(t, err, "link %d", i)

		flows = append(flows, sf)
	}

	return src, flows
}

// ids maps flows to their ID set for order-free comparison.
func ids(flows []*flow.SequenceFlow) map[string]bool {
	out := map[string]bool{}
	for _, f := range flows {
		out[f.ID()] = true
	}

	return out
}

// TestSelectOutgoing (SRD-046 T-1): every row of the activity-completion rule
// (token-flow.md) plus the classified error branches.
func TestSelectOutgoing(t *testing.T) {
	ctx := context.Background()

	t.Run("no flows — short-circuit", func(t *testing.T) {
		a, _ := linked(t)
		got, err := a.selectOutgoing(ctx, selectRe(t))
		require.NoError(t, err)
		require.Empty(t, got)
	})

	t.Run("single flow — short-circuit, no evaluation", func(t *testing.T) {
		a, fl := linked(t, constCond(t, false)) // even a false condition
		got, err := a.selectOutgoing(ctx, selectRe(t))
		require.NoError(t, err)
		require.Equal(t, ids(fl), ids(got))
	})

	t.Run("all unconditional — parallel split", func(t *testing.T) {
		a, fl := linked(t, nil, nil, nil)
		got, err := a.selectOutgoing(ctx, selectRe(t))
		require.NoError(t, err)
		require.Equal(t, ids(fl), ids(got))
	})

	t.Run("all conditional — true subset", func(t *testing.T) {
		a, fl := linked(t,
			constCond(t, true), constCond(t, false), constCond(t, true))
		got, err := a.selectOutgoing(ctx, selectRe(t))
		require.NoError(t, err)
		require.Equal(t,
			map[string]bool{fl[0].ID(): true, fl[2].ID(): true}, ids(got))
	})

	t.Run("mix — unconditional always, conditional when true", func(t *testing.T) {
		a, fl := linked(t, nil, constCond(t, false), constCond(t, true))
		got, err := a.selectOutgoing(ctx, selectRe(t))
		require.NoError(t, err)
		require.Equal(t,
			map[string]bool{fl[0].ID(): true, fl[2].ID(): true}, ids(got))
	})

	t.Run("default suppressed by a fired conditional", func(t *testing.T) {
		a, fl := linked(t, constCond(t, true), nil)
		require.NoError(t, a.SetDefaultFlow(fl[1].ID()))

		got, err := a.selectOutgoing(ctx, selectRe(t))
		require.NoError(t, err)
		require.Equal(t, map[string]bool{fl[0].ID(): true}, ids(got))
	})

	t.Run("default fires when no conditional fired", func(t *testing.T) {
		a, fl := linked(t, constCond(t, false), nil)
		require.NoError(t, a.SetDefaultFlow(fl[1].ID()))

		got, err := a.selectOutgoing(ctx, selectRe(t))
		require.NoError(t, err)
		require.Equal(t, map[string]bool{fl[1].ID(): true}, ids(got))
	})

	t.Run("default alongside unconditional, false conditional", func(t *testing.T) {
		// spec-literal: the default fires iff no CONDITIONAL fired —
		// unconditional flows don't suppress it (SRD-046 FR-1).
		a, fl := linked(t, nil, constCond(t, false), nil)
		require.NoError(t, a.SetDefaultFlow(fl[2].ID()))

		got, err := a.selectOutgoing(ctx, selectRe(t))
		require.NoError(t, err)
		require.Equal(t,
			map[string]bool{fl[0].ID(): true, fl[2].ID(): true}, ids(got))
	})

	t.Run("vacuous default — no conditionals at all", func(t *testing.T) {
		a, fl := linked(t, nil, nil)
		require.NoError(t, a.SetDefaultFlow(fl[1].ID()))

		got, err := a.selectOutgoing(ctx, selectRe(t))
		require.NoError(t, err)
		require.Equal(t, ids(fl), ids(got))
	})

	t.Run("zero selected — classified error", func(t *testing.T) {
		a, _ := linked(t, constCond(t, false), constCond(t, false))
		_, err := a.selectOutgoing(ctx, selectRe(t))
		require.ErrorContains(t, err, "no outgoing flow selected")
	})

	t.Run("non-bool condition — classified error", func(t *testing.T) {
		a, _ := linked(t, intCond(t), nil)
		_, err := a.selectOutgoing(ctx, selectRe(t))
		require.ErrorContains(t, err, "invalid condition expression type")
	})

	t.Run("failing evaluation — classified error", func(t *testing.T) {
		re := selectRe(t)
		re.EXPECT().Find(context.Background(), "missing").
			Return(nil, errFind).Maybe()

		a, _ := linked(t, failCond(t), nil)
		_, err := a.selectOutgoing(ctx, re)
		require.ErrorContains(t, err, "condition evaluation failed")
	})
}

// errFind is the stub error the failing-evaluation case returns.
var errFind = errors.New("missing datum")

// TestSetDefaultFlowHardening (SRD-046 T-2): the conditional-default
// rejection, the DefaultFlow getter, and the retained clear/unknown rules.
func TestSetDefaultFlowHardening(t *testing.T) {
	a, fl := linked(t, constCond(t, true), nil)

	t.Run("conditional default rejected", func(t *testing.T) {
		require.ErrorContains(t, a.SetDefaultFlow(fl[0].ID()),
			"must not carry a condition")
	})

	t.Run("unconditional default accepted + getter", func(t *testing.T) {
		require.NoError(t, a.SetDefaultFlow(fl[1].ID()))
		require.Equal(t, fl[1].ID(), a.DefaultFlow().ID())
	})

	t.Run("empty clears", func(t *testing.T) {
		require.NoError(t, a.SetDefaultFlow(""))
		require.Nil(t, a.DefaultFlow())
	})

	t.Run("unknown flow rejected", func(t *testing.T) {
		require.ErrorContains(t, a.SetDefaultFlow("nope"), "doesn't exist")
	})
}
