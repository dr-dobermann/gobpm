package localdispatcher_test

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/clock/clocktest"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	dgoexpr "github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	expengine "github.com/dr-dobermann/gobpm/pkg/model/expression/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/tasks"
	"github.com/dr-dobermann/gobpm/pkg/tasks/localdispatcher"
	"github.com/stretchr/testify/require"
)

// bodyPathExpr builds a rule Path that returns the body's value.
func bodyPathExpr(t *testing.T) data.FormalExpression {
	t.Helper()

	fe, err := dgoexpr.New(nil,
		data.MustItemDefinition(values.NewVariable("")),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			b, err := ds.Find(ctx, "body")
			if err != nil {
				return nil, err
			}

			return values.NewVariable(b.Value().Get(ctx)), nil
		})
	require.NoError(t, err)

	return fe
}

// missingPathExpr reads a datum the body never exposes, so it always errors.
func missingPathExpr(t *testing.T) data.FormalExpression {
	t.Helper()

	fe, err := dgoexpr.New(nil,
		data.MustItemDefinition(values.NewVariable("")),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			d, err := ds.Find(ctx, "nested")
			if err != nil {
				return nil, err
			}

			return values.NewVariable(d), nil
		})
	require.NoError(t, err)

	return fe
}

// TestDispatcherAppliesOutputMapping: under EngineAuthoritative the dispatcher
// shapes a raw completion body via Policy.OutputMapping into the final committed
// output, so the outcome carries the shaped data (SRD-039 M8, FR-1).
func TestDispatcherAppliesOutputMapping(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	sink := &recordSink{}
	d := localdispatcher.New(clocktest.New(base), time.Hour)
	d.BindSink(sink)
	d.BindExpressionEngine(expengine.New())

	policy := &tasks.Policy{OutputMapping: []tasks.OutputRule{
		{Path: bodyPathExpr(t), Var: "orderId"}}}
	enqueueAndLock(t, d, policy)

	body := data.MustItemDefinition(values.NewVariable("order-42"),
		foundation.WithID("body"))
	require.NoError(t, d.Complete(context.Background(), "j1", "w1", body))

	out := sink.last()
	require.Equal(t, tasks.OutcomeComplete, out.Kind())
	res := out.Output()
	require.Len(t, res, 1)
	require.Equal(t, "orderId", res[0].Name())
	require.Equal(t, "order-42", res[0].Value().Get(context.Background()))
}

// TestDispatcherCompleteNoMappingDirect: with no OutputMapping the dispatcher
// commits the raw output directly (the M3 direct-reconciliation default).
func TestDispatcherCompleteNoMappingDirect(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	sink := &recordSink{}
	d := localdispatcher.New(clocktest.New(base), time.Hour)
	d.BindSink(sink)

	enqueueAndLock(t, d, &tasks.Policy{})

	body := data.MustItemDefinition(values.NewVariable("v"),
		foundation.WithID("out"))
	require.NoError(t, d.Complete(context.Background(), "j1", "w1", body))

	res := sink.last().Output()
	require.Len(t, res, 1)
	require.Equal(t, "out", res[0].Name())
}

// TestDispatcherOutputMappingRequiredFaults: a required output path the body
// doesn't satisfy is a contract violation — the dispatcher reports a terminal
// technical fault, not a completion (SRD-039 §3.4).
func TestDispatcherOutputMappingRequiredFaults(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	sink := &recordSink{}
	d := localdispatcher.New(clocktest.New(base), time.Hour)
	d.BindSink(sink)
	d.BindExpressionEngine(expengine.New())

	policy := &tasks.Policy{OutputMapping: []tasks.OutputRule{
		{Path: missingPathExpr(t), Var: "v", Required: true}}}
	enqueueAndLock(t, d, policy)

	body := data.MustItemDefinition(values.NewVariable("x"),
		foundation.WithID("body"))
	require.NoError(t, d.Complete(context.Background(), "j1", "w1", body))

	require.Equal(t, tasks.OutcomeFault, sink.last().Kind())
}
