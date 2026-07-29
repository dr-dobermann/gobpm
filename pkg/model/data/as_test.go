package data_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/stretchr/testify/require"
)

type purchase struct {
	ID     string
	Amount int
}

// sid implements fmt.Stringer for the interface-payload case.
type sid string

func (s sid) String() string { return string(s) }

func TestAsScalar(t *testing.T) {
	ctx := context.Background()

	got, err := data.As[int](ctx, values.NewVariable(42))
	require.NoError(t, err)
	require.Equal(t, 42, got)
}

func TestAsStruct(t *testing.T) {
	ctx := context.Background()
	want := purchase{ID: "A-1", Amount: 150}

	got, err := data.As[purchase](ctx, values.NewVariable(want))
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestAsCollectionElement(t *testing.T) {
	ctx := context.Background()

	got, err := data.As[int](ctx, values.NewArray(7, 8, 9))
	require.NoError(t, err)
	require.Equal(t, 7, got)
}

func TestAsMap(t *testing.T) {
	ctx := context.Background()

	got, err := data.As[map[string]int](ctx,
		values.MustMap(map[string]int{"a": 1, "b": 2}))
	require.NoError(t, err)
	require.Equal(t, map[string]int{"a": 1, "b": 2}, got)
}

func TestAsRecord(t *testing.T) {
	ctx := context.Background()

	got, err := data.As[map[string]any](ctx,
		values.MustRecord(values.F("name", values.NewVariable("x"))))
	require.NoError(t, err)
	require.Equal(t, map[string]any{"name": "x"}, got)
}

func TestAsInterface(t *testing.T) {
	ctx := context.Background()

	got, err := data.As[fmt.Stringer](ctx, values.NewVariable(sid("S-9")))
	require.NoError(t, err)
	require.Equal(t, "S-9", got.String())
}

func TestAsNilValue(t *testing.T) {
	ctx := context.Background()

	_, err := data.As[int](ctx, nil)
	require.Error(t, err)

	var ae *errs.ApplicationError
	require.ErrorAs(t, err, &ae)
	require.True(t, ae.HasClass(errs.InvalidParameter))
	require.Contains(t, ae.Message, "As: a nil Value isn't allowed")
}

func TestAsMismatch(t *testing.T) {
	ctx := context.Background()

	_, err := data.As[int](ctx, values.NewVariable("oops"))
	require.Error(t, err)

	var ae *errs.ApplicationError
	require.ErrorAs(t, err, &ae)
	require.True(t, ae.HasClass(errs.TypeCastingError))
	require.Contains(t, ae.Message, "value holds string, not int")
	require.Equal(t, "string", ae.Details["held"])
	require.Equal(t, "int", ae.Details["requested"])
}

func TestAsMismatchInterfaceRequested(t *testing.T) {
	ctx := context.Background()

	_, err := data.As[data.Collection](ctx, values.NewVariable(1))
	require.Error(t, err)

	var ae *errs.ApplicationError
	require.ErrorAs(t, err, &ae)
	require.Contains(t, ae.Message, "not data.Collection")
	require.Equal(t, "data.Collection", ae.Details["requested"])
}
