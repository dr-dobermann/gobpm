package data_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/require"
)

// TestAssociationTransformationStructuralRead (SRD-042 T-11): a
// DataAssociation source resolves a structural path into a record-valued
// source — the input-mapping half of the seam integration (FR-4). A plain name
// returns the whole source; an unknown head errors.
func TestAssociationTransformationStructuralRead(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()

	target := data.MustItemAwareElement(
		data.MustItemDefinition(values.NewVariable(0),
			foundation.WithID("target")),
		data.UndefinedSrcState)

	orderRec := values.MustRecord(
		values.F("total", values.NewVariable(150)),
		values.F("items", values.NewArray[data.Value](
			values.MustRecord(values.F("price", values.NewVariable(50))),
		)),
	)

	a, err := data.NewAssociation(target,
		data.WithSource(
			data.MustItemAwareElement(
				data.MustItemDefinition(orderRec,
					foundation.WithID("order")),
				data.ReadyDataState)),
		foundation.WithID("assoc"))
	require.NoError(t, err)

	// structural read through the association source.
	d, err := a.Find(ctx, "order.items[0].price")
	require.NoError(t, err)
	require.Equal(t, 50, d.Value().Get(ctx))
	require.Equal(t, "order.items[0].price", d.Name())

	d, err = a.Find(ctx, "order.total")
	require.NoError(t, err)
	require.Equal(t, 150, d.Value().Get(ctx))

	// a plain name returns the whole source unchanged.
	d, err = a.Find(ctx, "order")
	require.NoError(t, err)
	require.NotNil(t, d.Value())

	// an unknown head errors.
	_, err = a.Find(ctx, "missing.x")
	require.Error(t, err)
}
