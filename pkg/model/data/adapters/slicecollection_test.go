package adapters_test

import (
	"io"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/stretchr/testify/require"
)

// itemsOf wraps the fixture and returns its items collection view + the host.
func itemsOf(t *testing.T) (*Order, data.Collection) {
	t.Helper()

	o, r := wrapOrder(t)

	items, err := r.Field(ctxb(), "items")
	require.NoError(t, err)

	return o, items.(data.Collection)
}

// TestSliceCollectionView (SRD-045 T-5): the full Collection contract over a
// LIVE slice — cursor ops, S2 SetAt bounds, mutations through the pointer,
// struct elements as live sub-records.
func TestSliceCollectionView(t *testing.T) {
	t.Run("reads: Count/GetKeys/GetAt/GetAll", func(t *testing.T) {
		_, col := itemsOf(t)

		require.Equal(t, 2, col.Count())
		require.Equal(t, []any{0, 1}, col.GetKeys())

		el, err := col.GetAt(ctxb(), 1)
		require.NoError(t, err)

		price, err := el.(data.Record).Field(ctxb(), "price")
		require.NoError(t, err)
		require.Equal(t, 100, price.Get(ctxb()))

		all := col.GetAll(ctxb())
		require.Len(t, all, 2)
		_, isRec := all[0].(data.Record)
		require.True(t, isRec)

		_, err = col.GetAt(ctxb(), "x")
		require.ErrorContains(t, err, "isn't an int")
		_, err = col.GetAt(ctxb(), 9)
		require.ErrorContains(t, err, "out of range")
	})

	t.Run("scalar elements come back raw", func(t *testing.T) {
		_, r := wrapOrder(t)

		tags, err := r.Field(ctxb(), "tags")
		require.NoError(t, err)

		el, err := tags.(data.Collection).GetAt(ctxb(), 0)
		require.NoError(t, err)
		require.Equal(t, "urgent", el)
	})

	t.Run("cursor: Index/GoTo/Next/Rewind/Get/Update", func(t *testing.T) {
		o, col := itemsOf(t)

		require.Equal(t, 0, col.Index())
		require.NoError(t, col.GoTo(-1)) // negative counts from the end
		require.Equal(t, 1, col.Index())

		require.ErrorIs(t, col.Next(data.StepForward), io.EOF)
		require.NoError(t, col.Next(data.StepBackward))
		require.Equal(t, 0, col.Index())

		got, ok := col.Get(ctxb()).(Item)
		require.True(t, ok)
		require.Equal(t, "widget", got.SKU)

		require.NoError(t,
			col.Update(ctxb(), Item{SKU: "widget", Price: 55}))
		require.Equal(t, 55, o.Items[0].Price) // the LIVE slice changed

		col.Rewind()
		require.Equal(t, 0, col.Index())

		require.ErrorContains(t, col.GoTo("x"), "isn't an int")
		require.ErrorContains(t, col.GoTo(9), "out of range")
	})

	t.Run("SetAt honors the S2 bounds", func(t *testing.T) {
		o, col := itemsOf(t)

		require.NoError(t,
			col.SetAt(ctxb(), 0, Item{SKU: "widget", Price: 60}))
		require.Equal(t, 60, o.Items[0].Price)

		require.NoError(t, // == len appends
			col.SetAt(ctxb(), 2, Item{SKU: "bolt", Price: 5}))
		require.Len(t, o.Items, 3)

		require.ErrorContains(t, // past len is a hole
			col.SetAt(ctxb(), 9, Item{}), "out of range")
		require.ErrorContains(t,
			col.SetAt(ctxb(), "x", Item{}), "isn't an int")
		require.ErrorContains(t, // type clash
			col.SetAt(ctxb(), 0, "not an item"), "isn't assignable")
	})

	t.Run("Add/Insert/Delete/Clear mutate the live slice", func(t *testing.T) {
		o, col := itemsOf(t)

		require.NoError(t, col.Add(ctxb(), Item{SKU: "nut", Price: 2}))
		require.Len(t, o.Items, 3)

		require.NoError(t, col.Insert(ctxb(), Item{SKU: "first", Price: 1}, 0))
		require.Equal(t, "first", o.Items[0].SKU)
		require.Len(t, o.Items, 4)

		require.ErrorContains(t,
			col.Insert(ctxb(), Item{}, 9), "out of range")
		require.ErrorContains(t,
			col.Insert(ctxb(), Item{}, "x"), "isn't an int")

		require.NoError(t, col.Delete(ctxb(), 0))
		require.Equal(t, "widget", o.Items[0].SKU)

		require.ErrorContains(t, col.Delete(ctxb(), "x"), "isn't an int")

		col.Clear()
		require.Empty(t, o.Items)
		require.Equal(t, -1, col.Index())

		// empty-collection contracts
		require.ErrorContains(t,
			col.Update(ctxb(), Item{}), "empty")
		require.Panics(t, func() { col.Get(ctxb()) })
		col.Rewind() // no-op on empty

		// Add on empty seats the cursor (and SetAt at 0 appends)
		require.NoError(t, col.SetAt(ctxb(), 0, Item{SKU: "seed", Price: 1}))
		require.Equal(t, 0, col.Index())
	})

	t.Run("Delete re-seats a trailing cursor", func(t *testing.T) {
		_, col := itemsOf(t)

		require.NoError(t, col.GoTo(1))
		require.NoError(t, col.Delete(ctxb(), 1))
		require.Equal(t, 0, col.Index())
	})

	t.Run("a data.Value lands via its Get (the SetPath shape)", func(t *testing.T) {
		_, r := wrapOrder(t)

		tags, err := r.Field(ctxb(), "tags")
		require.NoError(t, err)

		col := tags.(data.Collection)
		require.NoError(t,
			col.SetAt(ctxb(), 0, values.NewVariable("bulk")))

		el, err := col.GetAt(ctxb(), 0)
		require.NoError(t, err)
		require.Equal(t, "bulk", el)
	})

	t.Run("collection Value surface: Type/Lock/Unlock", func(t *testing.T) {
		_, col := itemsOf(t)

		v := col.(data.Value)
		require.Equal(t, "[]adapters_test.Item", v.Type())
		v.Lock()
		v.Unlock()
	})
}
