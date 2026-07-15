package adapters_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/adapters"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/stretchr/testify/require"
)

// holder exercises the pointer-sharing clone rule.
type holder struct {
	Ref *Shipping `gobpm:"ref"`
	N   int       `gobpm:"n"`
}

// TestClone (SRD-045 T-6): value-copy independence — struct fields and slice
// elements detach; pointer fields remain shared (the documented shallow
// rule); passthrough values clone via their own Clone.
func TestClone(t *testing.T) {
	t.Run("scalar and slice independence", func(t *testing.T) {
		o, r := wrapOrder(t)

		c := r.Clone().(data.Record)

		// mutate the clone — the original must not move
		require.NoError(t, c.SetField(ctxb(), "total", values.NewVariable(1)))

		items, err := c.Field(ctxb(), "items")
		require.NoError(t, err)
		require.NoError(t, items.(data.Collection).SetAt(ctxb(), 0,
			Item{SKU: "widget", Price: 999}))

		require.Equal(t, 150, o.Total)
		require.Equal(t, 50, o.Items[0].Price)

		// and the reverse: mutating the original leaves the clone intact
		o.Total = 7
		f, err := c.Field(ctxb(), "total")
		require.NoError(t, err)
		require.Equal(t, 1, f.Get(ctxb()))
	})

	t.Run("passthrough values clone via their own Clone", func(t *testing.T) {
		o, r := wrapOrder(t)

		c := r.Clone().(data.Record)

		extra, err := c.Field(ctxb(), "extra")
		require.NoError(t, err)
		require.NoError(t, extra.(data.Record).SetField(ctxb(), "note",
			values.NewVariable("changed")))

		note, err := o.Extra.(data.Record).Field(ctxb(), "note")
		require.NoError(t, err)
		require.Equal(t, "expedite", note.Get(ctxb())) // original untouched
	})

	t.Run("pointer fields remain shared (documented shallow)", func(t *testing.T) {
		h := &holder{Ref: &Shipping{Zone: "Z-1"}, N: 1}

		v, err := adapters.Wrap(h)
		require.NoError(t, err)

		c := v.Clone().(data.Record)

		// mutate THROUGH the clone's pointer field — visible on both
		ref, err := c.Field(ctxb(), "ref")
		require.NoError(t, err)
		require.NoError(t, ref.(data.Record).SetField(ctxb(), "zone",
			values.NewVariable("Z-2")))

		require.Equal(t, "Z-2", h.Ref.Zone) // shared pointee
	})

	t.Run("leaf and collection views clone detached", func(t *testing.T) {
		o, r := wrapOrder(t)

		total, err := r.Field(ctxb(), "total")
		require.NoError(t, err)

		lc := total.Clone()
		require.NoError(t, lc.Update(ctxb(), values.NewVariable(3)))
		require.Equal(t, 150, o.Total) // detached

		items, err := r.Field(ctxb(), "items")
		require.NoError(t, err)

		cc := items.(data.Value).Clone().(data.Collection)
		require.NoError(t, cc.SetAt(ctxb(), 0, Item{SKU: "x", Price: 1}))
		require.Equal(t, 50, o.Items[0].Price) // detached
	})
}
