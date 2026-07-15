package adapters_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/adapters"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/stretchr/testify/require"
)

// tick is a second Register-ed type, used as a slice element.
type tick struct {
	n int
}

// TestEdgeCases sweeps the branches the mainline scenarios don't reach:
// tag options, custom/pointer slice elements, coercion failures, cursor
// bounds, nested-build failures, and the Lock/Unlock surfaces.
func TestEdgeCases(t *testing.T) {
	t.Run("tag with options — name before the comma", func(t *testing.T) {
		type tagged struct {
			A int `gobpm:"alpha,future-option"`
		}

		v, err := adapters.Wrap(&tagged{A: 1})
		require.NoError(t, err)
		require.Equal(t, []string{"alpha"}, v.(data.Record).Keys())
	})

	t.Run("leaf Update type clash", func(t *testing.T) {
		_, r := wrapOrder(t)

		total, err := r.Field(ctxb(), "total")
		require.NoError(t, err)
		require.ErrorContains(t,
			total.Update(ctxb(), "NaN"), "isn't assignable")
		require.ErrorContains(t,
			total.Update(ctxb(), nil), "isn't assignable")
	})

	t.Run("Lock/Unlock surfaces", func(t *testing.T) {
		_, r := wrapOrder(t)

		r.(data.Value).Lock()
		r.(data.Value).Unlock()

		total, err := r.Field(ctxb(), "total")
		require.NoError(t, err)
		total.Lock()
		total.Unlock()
	})

	t.Run("slice of Register-ed elements", func(t *testing.T) {
		require.NoError(t, adapters.Register(func(v *tick) data.Value {
			return values.MustRecord(values.F("n", values.NewVariable(v.n)))
		}))

		type feed struct {
			Ticks []tick `gobpm:"ticks"`
		}

		v, err := adapters.Wrap(&feed{Ticks: []tick{{n: 3}}})
		require.NoError(t, err)

		ticks, err := v.(data.Record).Field(ctxb(), "ticks")
		require.NoError(t, err)

		el, err := ticks.(data.Collection).GetAt(ctxb(), 0)
		require.NoError(t, err)

		n, err := el.(data.Record).Field(ctxb(), "n")
		require.NoError(t, err)
		require.Equal(t, 3, n.Get(ctxb()))
	})

	t.Run("slice of pointer elements — deref and nil", func(t *testing.T) {
		type fleet struct {
			Ships []*Shipping `gobpm:"ships"`
		}

		f := &fleet{Ships: []*Shipping{{Zone: "Z-1"}, nil}}

		v, err := adapters.Wrap(f)
		require.NoError(t, err)

		ships, err := v.(data.Record).Field(ctxb(), "ships")
		require.NoError(t, err)

		col := ships.(data.Collection)

		el, err := col.GetAt(ctxb(), 0)
		require.NoError(t, err)

		zone, err := el.(data.Record).Field(ctxb(), "zone")
		require.NoError(t, err)
		require.Equal(t, "Z-1", zone.Get(ctxb()))

		_, err = col.GetAt(ctxb(), 1) // the nil element
		require.ErrorContains(t, err, "nil")

		all := col.GetAll(ctxb()) // falls back to the raw nil element
		require.Len(t, all, 2)
	})

	t.Run("collection coercion and cursor bounds", func(t *testing.T) {
		_, col := itemsOf(t)

		require.ErrorContains(t,
			col.(data.Value).Update(ctxb(), "junk"), "isn't assignable")
		require.ErrorContains(t,
			col.Add(ctxb(), "junk"), "isn't assignable")
		require.ErrorContains(t,
			col.Insert(ctxb(), "junk", 0), "isn't assignable")

		require.NoError(t, col.GoTo(0))
		require.ErrorContains(t, // backward past the head
			col.Next(data.StepBackward), "out of range")
	})

	t.Run("GetAt on an empty collection", func(t *testing.T) {
		type box struct {
			Bits []int `gobpm:"bits"`
		}

		v, err := adapters.Wrap(&box{})
		require.NoError(t, err)

		bits, err := v.(data.Record).Field(ctxb(), "bits")
		require.NoError(t, err)

		_, err = bits.(data.Collection).GetAt(ctxb(), 0)
		require.ErrorContains(t, err, "empty")
	})

	t.Run("nested type failing its own build surfaces at Field", func(t *testing.T) {
		type badChild struct {
			X int `gobpm:"a.b"` // path-illegal name
		}
		type parent struct {
			Kid badChild `gobpm:"kid"`
		}

		v, err := adapters.Wrap(&parent{}) // parent builds fine
		require.NoError(t, err)

		_, err = v.(data.Record).Field(ctxb(), "kid") // the child build fails
		require.Error(t, err)
	})

	t.Run("untagged exported field keeps its Go name", func(t *testing.T) {
		type plain struct {
			Amount int
		}

		v, err := adapters.Wrap(&plain{Amount: 2})
		require.NoError(t, err)
		require.Equal(t, []string{"Amount"}, v.(data.Record).Keys())
	})

	t.Run("slice element type failing its build surfaces at GetAt", func(t *testing.T) {
		type badElem struct {
			X int `gobpm:"a.b"`
		}
		type bag struct {
			Es []badElem `gobpm:"es"`
		}

		v, err := adapters.Wrap(&bag{Es: []badElem{{X: 1}}})
		require.NoError(t, err)

		es, err := v.(data.Record).Field(ctxb(), "es")
		require.NoError(t, err)

		_, err = es.(data.Collection).GetAt(ctxb(), 0)
		require.Error(t, err)
	})

	t.Run("empty-collection seating: Add, Insert; Delete to empty", func(t *testing.T) {
		type box struct {
			Bits []int `gobpm:"bits"`
		}

		v, err := adapters.Wrap(&box{})
		require.NoError(t, err)

		bits, err := v.(data.Record).Field(ctxb(), "bits")
		require.NoError(t, err)

		col := bits.(data.Collection)
		require.Equal(t, -1, col.Index())

		require.NoError(t, col.Add(ctxb(), 1)) // Add on empty seats the cursor
		require.Equal(t, 0, col.Index())

		require.ErrorContains(t, // out-of-range delete
			col.Delete(ctxb(), 9), "out of range")

		require.NoError(t, col.Delete(ctxb(), 0)) // delete to empty parks it
		require.Equal(t, -1, col.Index())

		require.NoError(t, col.Insert(ctxb(), 5, 0)) // Insert on empty seats
		require.Equal(t, 0, col.Index())
	})

	t.Run("Clone with a nil slice field", func(t *testing.T) {
		o := testOrder()
		o.Items = nil

		v, err := adapters.Wrap(o)
		require.NoError(t, err)
		require.NotNil(t, v.Clone())
	})

	t.Run("map-merge into a failing field view errs", func(t *testing.T) {
		o := testOrder()
		o.Ship = nil // the sub-record view is unreachable

		v, err := adapters.Wrap(o)
		require.NoError(t, err)

		err = v.Update(ctxb(),
			map[string]any{"ship": map[string]any{"zone": "Z-1"}})
		require.ErrorContains(t, err, "nil")
	})
}
