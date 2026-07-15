package adapters_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/stretchr/testify/require"
)

// TestSeamsOverWrappedValues (SRD-045 T-7): the S1 read walk, the S2 write
// path, and the S3 commit-diff all navigate a wrapped struct with ZERO seam
// edits — the FR-6 zero-engine-change proof at the data layer.
func TestSeamsOverWrappedValues(t *testing.T) {
	t.Run("WalkSteps reads into the wrapped struct (S1)", func(t *testing.T) {
		_, r := wrapOrder(t)

		leaf, err := data.WalkSteps(ctxb(), r, []data.Step{
			{Field: "items"}, {Index: 0}, {Field: "price"}})
		require.NoError(t, err)
		require.Equal(t, 50, leaf.Get(ctxb()))

		// through the pointer-to-struct field too
		zone, err := data.WalkSteps(ctxb(), r, []data.Step{
			{Field: "ship"}, {Field: "zone"}})
		require.NoError(t, err)
		require.Equal(t, "Z-9", zone.Get(ctxb()))
	})

	t.Run("SetPath writes into the LIVE struct (S2)", func(t *testing.T) {
		o, r := wrapOrder(t)

		require.NoError(t,
			values.SetPath(ctxb(), r, "total", values.NewVariable(175)))
		require.Equal(t, 175, o.Total)

		require.NoError(t, values.SetPath(ctxb(), r,
			"items[0].price", values.NewVariable(60)))
		require.Equal(t, 60, o.Items[0].Price)

		// the typed-target posture: no auto-vivify of unknown fields
		require.Error(t, values.SetPath(ctxb(), r,
			"nope.x", values.NewVariable(1)))
	})

	t.Run("DiffValues diffs two wrapped states (S3)", func(t *testing.T) {
		_, r := wrapOrder(t)

		prior := r.(data.Value).Clone()

		require.NoError(t,
			values.SetPath(ctxb(), r, "total", values.NewVariable(200)))

		items, err := r.Field(ctxb(), "items")
		require.NoError(t, err)
		require.NoError(t, items.(data.Collection).SetAt(ctxb(), 2,
			Item{SKU: "bolt", Price: 5}))

		changes := data.DiffValues("order", prior, r.(data.Value))
		require.Equal(t, []data.Change{
			{Path: "order.total", Type: data.ValueUpdated},
			{Path: "order.items[2]", Type: data.ValueAdded},
		}, changes)
	})

	t.Run("DiffValues across tiers — wrapped vs dynamic", func(t *testing.T) {
		// both are Records, so the diff descends regardless of tier
		w, err := wrapOf(t, &Shipping{Zone: "Z-1"})
		require.NoError(t, err)

		d := values.MustRecord(values.F("zone", values.NewVariable("Z-2")))

		changes := data.DiffValues("ship", w, d)
		require.Equal(t, []data.Change{
			{Path: "ship.zone", Type: data.ValueUpdated}}, changes)
	})
}
