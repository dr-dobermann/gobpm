package msgflow_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	exprgo "github.com/dr-dobermann/gobpm/pkg/model/expression/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/msgflow"
	"github.com/stretchr/testify/require"
)

// fieldExpr builds a MessagePath that reads field from the payload map (which
// the payload source exposes under the message item id "order"). missing addr
// makes Find fail.
func fieldExpr(t *testing.T, field, addr string) data.FormalExpression {
	t.Helper()

	return goexpr.Must(nil,
		data.MustItemDefinition(values.NewVariable("")),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			d, err := ds.Find(ctx, addr)
			if err != nil {
				return nil, err
			}

			m, _ := d.Value().Get(ctx).(map[string]any)

			return values.NewVariable(fmt.Sprint(m[field])), nil
		})
}

// corrKey builds a CorrelationKey over msg with one property per (field, addr).
func corrKey(
	t *testing.T, msg *bpmncommon.Message, fields [][2]string,
) *bpmncommon.CorrelationKey {
	t.Helper()

	props := make([]bpmncommon.CorrelationProperty, 0, len(fields))
	for _, fa := range fields {
		re, err := bpmncommon.NewCorrelationPropertyRetrievalExpression(
			fieldExpr(t, fa[0], fa[1]), msg)
		require.NoError(t, err)

		prop, err := bpmncommon.NewCorrelationProperty(fa[0], "string",
			[]bpmncommon.CorrelationPropertyRetrievalExpression{*re})
		require.NoError(t, err)

		props = append(props, *prop)
	}

	key, err := bpmncommon.NewCorrelationKey("orderKey", props)
	require.NoError(t, err)

	return key
}

func msg(t *testing.T) *bpmncommon.Message {
	t.Helper()

	return bpmncommon.MustMessage("order placed",
		data.MustItemDefinition(values.NewVariable(map[string]any{}),
			foundation.WithID("order")))
}

func TestDeriveKey(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()
	eng := exprgo.New()
	m := msg(t)
	payload := map[string]any{"orderId": "ORD-1", "customerId": "C-9"}

	t.Run("composite key from all properties", func(t *testing.T) {
		key := corrKey(t, m, [][2]string{{"orderId", "order"}, {"customerId", "order"}})

		k, ok, err := msgflow.DeriveKey(ctx, eng, key, m, payload)
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, "ORD-1\x1fC-9", k)
	})

	t.Run("property with no expression for the message is invalid", func(t *testing.T) {
		// the retrieval expression's MessageRef is a different message.
		other := bpmncommon.MustMessage("other",
			data.MustItemDefinition(values.NewVariable(map[string]any{}),
				foundation.WithID("order")))
		key := corrKey(t, other, [][2]string{{"orderId", "order"}})

		_, ok, err := msgflow.DeriveKey(ctx, eng, key, m, payload)
		require.NoError(t, err)
		require.False(t, ok)
	})

	t.Run("evaluation failure surfaces an error", func(t *testing.T) {
		// the expression reads a non-existent source address.
		key := corrKey(t, m, [][2]string{{"orderId", "nope"}})

		_, ok, err := msgflow.DeriveKey(ctx, eng, key, m, payload)
		require.Error(t, err)
		require.False(t, ok)
	})

	t.Run("nil arguments rejected", func(t *testing.T) {
		key := corrKey(t, m, [][2]string{{"orderId", "order"}})

		_, _, err := msgflow.DeriveKey(ctx, nil, key, m, payload)
		require.Error(t, err)

		_, _, err = msgflow.DeriveKey(ctx, eng, nil, m, payload)
		require.Error(t, err)

		_, _, err = msgflow.DeriveKey(ctx, eng, key, nil, payload)
		require.Error(t, err)
	})
}
