package gateways

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/generated/mockrenv"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	dgexpr "github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// nonBoolEngine returns a STRING for every expression, whatever the expression
// declares. The built-in engine type-checks its own result, so this is how a
// third-party engine's contract violation reaches the condition check — the
// branch data.As guards (ADR-034 v.1 §5, FIX-034 §3.2.3).
type nonBoolEngine struct{}

func (nonBoolEngine) Type() string        { return "##NonBool" }
func (nonBoolEngine) Languages() []string { return []string{dgexpr.Language} }

func (nonBoolEngine) Evaluate(
	context.Context, data.FormalExpression, data.Source,
) (data.Value, error) {
	return values.NewVariable("not a bool"), nil
}

func TestGatewayConditionRejectsANonBooleanResult(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	re := mockrenv.NewMockRuntimeEnvironment(t)
	re.EXPECT().ExpressionEngine().Return(nonBoolEngine{}).Maybe()

	// A bool-DECLARING condition, so the ResultType guard above passes and the
	// engine's answer is what fails the check.
	cond, err := dgexpr.New(nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(context.Context, data.Source) (data.Value, error) {
			return values.NewVariable(true), nil
		})
	require.NoError(t, err)

	g, err := NewExclusiveGateway()
	require.NoError(t, err)

	target, err := NewParallelGateway()
	require.NoError(t, err)

	sf, err := flow.Link(g, target, flow.WithCondition(cond))
	require.NoError(t, err)

	_, cerr := g.checkCondition(context.Background(), re, cond, sf)
	require.Error(t, cerr)

	var ae *errs.ApplicationError

	require.ErrorAs(t, cerr, &ae)
	require.True(t, ae.HasClass(errs.TypeCastingError),
		"an engine that ignores the declared result type is reported, not "+
			"asserted into a panic")
	require.Contains(t, cerr.Error(), "not boolean")
}
