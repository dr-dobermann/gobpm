package adapters_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/adapters"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/stretchr/testify/require"
)

// The shared test fixture: a host order type exercising every field kind —
// tag rename, exclusion, unexported, nested struct, pointer-to-struct,
// slice-of-structs, scalar slice, passthrough (data.Value field), and an
// opaque map leaf.
type Item struct {
	SKU   string `gobpm:"sku"`
	Price int    `gobpm:"price"`
}

type Shipping struct {
	Zone string `gobpm:"zone"`
}

type Order struct {
	ID       string         `gobpm:"id"`
	Total    int            `gobpm:"total"`
	Items    []Item         `gobpm:"items"`
	Tags     []string       `gobpm:"tags"`
	Ship     *Shipping      `gobpm:"ship"`
	Extra    data.Value     `gobpm:"extra"`
	Meta     map[string]int `gobpm:"meta"`
	Secret   string         `gobpm:"-"`
	internal string         //nolint:unused // unexported — never visible
}

// testOrder builds the standard fixture instance.
func testOrder() *Order {
	return &Order{
		ID:    "A-1",
		Total: 150,
		Items: []Item{{SKU: "widget", Price: 50}, {SKU: "gadget", Price: 100}},
		Tags:  []string{"urgent"},
		Ship:  &Shipping{Zone: "Z-9"},
		Extra: values.MustRecord(
			values.F("note", values.NewVariable("expedite"))),
		Meta:   map[string]int{"weight": 3},
		Secret: "hidden",
	}
}

// TestWrapValidation (SRD-045 T-1): every invalid Wrap input is a classified,
// self-identifying error; MustWrap panics on the same.
func TestWrapValidation(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		_, err := adapters.Wrap(nil)
		require.ErrorContains(t, err, "Wrap")
	})

	t.Run("non-pointer", func(t *testing.T) {
		_, err := adapters.Wrap(Order{})
		require.ErrorContains(t, err, "pointer")
	})

	t.Run("pointer to non-struct (unregistered)", func(t *testing.T) {
		x := 5
		_, err := adapters.Wrap(&x)
		require.ErrorContains(t, err, "not a struct")
	})

	t.Run("nil pointer", func(t *testing.T) {
		var o *Order
		_, err := adapters.Wrap(o)
		require.ErrorContains(t, err, "nil")
	})

	t.Run("valid wrap satisfies Record", func(t *testing.T) {
		v, err := adapters.Wrap(testOrder())
		require.NoError(t, err)

		_, ok := v.(data.Record)
		require.True(t, ok)
	})

	t.Run("MustWrap panics on invalid, returns on valid", func(t *testing.T) {
		require.Panics(t, func() { adapters.MustWrap(nil) })
		require.NotNil(t, adapters.MustWrap(testOrder()))
	})
}

// ctxb is a shorthand for the background context used across the tests.
func ctxb() context.Context { return context.Background() }
