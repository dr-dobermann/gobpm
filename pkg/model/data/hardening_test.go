package data_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockdata"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/stretchr/testify/require"
)

// TestNewExpression covers the natural-language Expression constructor — the
// success path and the base-element build failure (empty id).
func TestNewExpression(t *testing.T) {
	e, err := data.NewExpression(foundation.WithID("expr-1"))
	require.NoError(t, err)
	require.Equal(t, "expr-1", e.ID())

	_, err = data.NewExpression(foundation.WithID(""))
	require.Error(t, err)
}

// TestOpposite covers the Direction reversal helper.
func TestOpposite(t *testing.T) {
	require.Equal(t, data.Output, data.Opposite(data.Input))
	require.Equal(t, data.Input, data.Opposite(data.Output))
}

// TestAssociationErrorPaths covers the remaining untested guard/error branches
// of Association construction, option validation, and source access.
func TestAssociationErrorPaths(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	newTarget := func() *data.ItemAwareElement {
		return data.MustItemAwareElement(
			data.MustItemDefinition(values.NewVariable(0),
				foundation.WithID("out")),
			data.ReadyDataState)
	}
	src := func(id string, v int, st *data.SrcState) *data.ItemAwareElement {
		return data.MustItemAwareElement(
			data.MustItemDefinition(values.NewVariable(v),
				foundation.WithID(id)),
			st)
	}

	// invalid option type (neither asscOption nor foundation.BaseOption)
	_, err := data.NewAssociation(newTarget(), options.WithName("nope"))
	require.Error(t, err)

	// WithSource: nil source, and duplicate ItemDefinition id
	_, err = data.NewAssociation(newTarget(), data.WithSource(nil))
	require.Error(t, err)

	_, err = data.NewAssociation(newTarget(),
		data.WithSource(src("dup", 1, data.ReadyDataState)),
		data.WithSource(src("dup", 2, data.ReadyDataState)))
	require.Error(t, err)

	// WithTransformation: nil expression, and set twice
	_, err = data.NewAssociation(newTarget(), data.WithTransformation(nil))
	require.Error(t, err)

	_, err = data.NewAssociation(newTarget(),
		data.WithTransformation(mockdata.NewMockFormalExpression(t)),
		data.WithTransformation(mockdata.NewMockFormalExpression(t)))
	require.Error(t, err)

	// UpdateSource with a nil ItemDefinition
	a, err := data.NewAssociation(newTarget(),
		data.WithSource(src("s", 5, data.ReadyDataState)))
	require.NoError(t, err)
	require.Error(t, a.UpdateSource(context.Background(), nil, false))

	// Find on an unknown source id
	_, err = a.Find(context.Background(), "missing")
	require.Error(t, err)

	// calculate on a single non-Ready source → not-ready error surfaced via Value
	aNR, err := data.NewAssociation(newTarget(),
		data.WithSource(src("nr", 7, data.UndefinedSrcState)))
	require.NoError(t, err)
	_, err = aNR.Value(context.Background())
	require.Error(t, err)
}
