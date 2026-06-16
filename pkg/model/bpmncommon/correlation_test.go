package bpmncommon_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/require"
)

// corrMessage builds a message for the correlation constructor tests.
func corrMessage(t *testing.T, name string) *bpmncommon.Message {
	t.Helper()

	return bpmncommon.MustMessage(name,
		data.MustItemDefinition(values.NewVariable(""),
			foundation.WithID("order")))
}

// corrExpr builds a trivial MessagePath expression.
func corrExpr(t *testing.T) data.FormalExpression {
	t.Helper()

	return goexpr.Must(nil,
		data.MustItemDefinition(values.NewVariable("")),
		func(_ context.Context, _ data.Source) (data.Value, error) {
			return values.NewVariable("x"), nil
		})
}

func TestNewCorrelationPropertyRetrievalExpression(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	msg := corrMessage(t, "order placed")

	re, err := bpmncommon.NewCorrelationPropertyRetrievalExpression(
		corrExpr(t), msg)
	require.NoError(t, err)
	require.NotNil(t, re)
	require.Equal(t, msg, re.MessageRef)

	// nil message path / message ref rejected.
	_, err = bpmncommon.NewCorrelationPropertyRetrievalExpression(nil, msg)
	require.Error(t, err)

	_, err = bpmncommon.NewCorrelationPropertyRetrievalExpression(
		corrExpr(t), nil)
	require.Error(t, err)

	// a failing base option (empty id) is surfaced.
	_, err = bpmncommon.NewCorrelationPropertyRetrievalExpression(
		corrExpr(t), msg, foundation.WithID(" "))
	require.Error(t, err)
}

func TestNewCorrelationProperty(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	re, err := bpmncommon.NewCorrelationPropertyRetrievalExpression(
		corrExpr(t), corrMessage(t, "order placed"))
	require.NoError(t, err)

	exprs := []bpmncommon.CorrelationPropertyRetrievalExpression{*re}

	prop, err := bpmncommon.NewCorrelationProperty("orderId", "string", exprs)
	require.NoError(t, err)
	require.Equal(t, "orderId", prop.Name)
	require.Len(t, prop.Expressions, 1)

	// blank name / empty expressions rejected.
	_, err = bpmncommon.NewCorrelationProperty("  ", "string", exprs)
	require.Error(t, err)

	_, err = bpmncommon.NewCorrelationProperty("orderId", "string", nil)
	require.Error(t, err)

	// a failing base option (empty id) is surfaced.
	_, err = bpmncommon.NewCorrelationProperty("orderId", "string", exprs,
		foundation.WithID(" "))
	require.Error(t, err)
}

func TestNewCorrelationKey(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	re, err := bpmncommon.NewCorrelationPropertyRetrievalExpression(
		corrExpr(t), corrMessage(t, "order placed"))
	require.NoError(t, err)

	prop, err := bpmncommon.NewCorrelationProperty("orderId", "string",
		[]bpmncommon.CorrelationPropertyRetrievalExpression{*re})
	require.NoError(t, err)

	props := []bpmncommon.CorrelationProperty{*prop}

	key, err := bpmncommon.NewCorrelationKey("orderKey", props)
	require.NoError(t, err)
	require.Equal(t, "orderKey", key.Name)
	require.Len(t, key.Properties, 1)

	// blank name / empty properties rejected.
	_, err = bpmncommon.NewCorrelationKey("   ", props)
	require.Error(t, err)

	_, err = bpmncommon.NewCorrelationKey("orderKey", nil)
	require.Error(t, err)

	// a failing base option (empty id) is surfaced.
	_, err = bpmncommon.NewCorrelationKey("orderKey", props,
		foundation.WithID(" "))
	require.Error(t, err)
}
