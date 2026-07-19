package adapters_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/adapters"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/stretchr/testify/require"
)

// wrapOrder wraps the standard fixture and returns both views.
func wrapOrder(t *testing.T) (*Order, data.Record) {
	t.Helper()

	o := testOrder()
	v, err := adapters.Wrap(o)
	require.NoError(t, err)

	return o, v.(data.Record)
}

// TestStructRecordRead (SRD-045 T-3): Keys order/renames/exclusions, live
// Field views per kind, native-object Get, cached Type.
func TestStructRecordRead(t *testing.T) {
	o, r := wrapOrder(t)

	t.Run("Keys — declaration order, renamed, exclusions absent", func(t *testing.T) {
		require.Equal(t,
			[]string{"id", "total", "items", "tags", "ship", "extra", "meta"},
			r.Keys())
	})

	t.Run("scalar leaf field", func(t *testing.T) {
		f, err := r.Field(ctxb(), "total")
		require.NoError(t, err)
		require.Equal(t, 150, f.Get(ctxb()))
		require.Equal(t, "int", f.Type())
	})

	t.Run("pointer-to-struct field — live sub-record", func(t *testing.T) {
		ship, err := r.Field(ctxb(), "ship")
		require.NoError(t, err)

		zone, err := ship.(data.Record).Field(ctxb(), "zone")
		require.NoError(t, err)
		require.Equal(t, "Z-9", zone.Get(ctxb()))
	})

	t.Run("slice field — collection view", func(t *testing.T) {
		items, err := r.Field(ctxb(), "items")
		require.NoError(t, err)

		col, ok := items.(data.Collection)
		require.True(t, ok)
		require.Equal(t, 2, col.Count())
	})

	t.Run("passthrough field — the held Value itself", func(t *testing.T) {
		extra, err := r.Field(ctxb(), "extra")
		require.NoError(t, err)

		note, err := extra.(data.Record).Field(ctxb(), "note")
		require.NoError(t, err)
		require.Equal(t, "expedite", note.Get(ctxb()))
	})

	t.Run("string-keyed map field — a live navigable data.Map", func(t *testing.T) {
		// SRD-047 flips SRD-045's "map field — opaque leaf" pin: a
		// map[string]int field is now the map kind, navigable over the live map.
		meta, err := r.Field(ctxb(), "meta")
		require.NoError(t, err)

		m, isMap := meta.(data.Map)
		require.True(t, isMap)
		require.Equal(t, []string{"weight"}, m.Keys())

		v, err := m.Entry(ctxb(), "weight")
		require.NoError(t, err)
		require.Equal(t, 3, v)

		// a write goes through to the LIVE struct field
		require.NoError(t, m.SetEntry(ctxb(), "height", 7))
		require.Equal(t, 7, o.Meta["height"])
	})

	t.Run("excluded and unexported are unknown", func(t *testing.T) {
		_, err := r.Field(ctxb(), "Secret")
		require.ErrorContains(t, err, "no field")
		_, err = r.Field(ctxb(), "internal")
		require.Error(t, err)
	})

	t.Run("nil pointer-to-struct field errs", func(t *testing.T) {
		o2 := testOrder()
		o2.Ship = nil
		v, err := adapters.Wrap(o2)
		require.NoError(t, err)

		_, err = v.(data.Record).Field(ctxb(), "ship")
		require.ErrorContains(t, err, "nil")
	})

	t.Run("nil passthrough field errs", func(t *testing.T) {
		o2 := testOrder()
		o2.Extra = nil
		v, err := adapters.Wrap(o2)
		require.NoError(t, err)

		_, err = v.(data.Record).Field(ctxb(), "extra")
		require.ErrorContains(t, err, "no value")
	})

	t.Run("Get returns the native Go object", func(t *testing.T) {
		got, ok := r.Get(ctxb()).(Order)
		require.True(t, ok)
		require.Equal(t, o.ID, got.ID)
		require.Equal(t, o.Total, got.Total)
	})

	t.Run("Type is the cached Go type name", func(t *testing.T) {
		require.Equal(t, "adapters_test.Order", r.Type())
	})
}

// TestStructRecordWrite (SRD-045 T-4): SetField write-through with typed
// rejection; Update by struct replace and by cross-tier map merge.
func TestStructRecordWrite(t *testing.T) {
	t.Run("SetField writes through to the live struct", func(t *testing.T) {
		o, r := wrapOrder(t)

		require.NoError(t,
			r.SetField(ctxb(), "total", values.NewVariable(175)))
		require.Equal(t, 175, o.Total) // the LIVE struct changed
	})

	t.Run("SetField rejections", func(t *testing.T) {
		_, r := wrapOrder(t)

		require.ErrorContains(t,
			r.SetField(ctxb(), "nope", values.NewVariable(1)), "no field")
		require.ErrorContains(t,
			r.SetField(ctxb(), "total", values.NewVariable("NaN")),
			"isn't assignable")
		require.ErrorContains(t,
			r.SetField(ctxb(), "total", nil), "nil")
	})

	t.Run("Update replaces with the same struct type", func(t *testing.T) {
		o, r := wrapOrder(t)

		require.NoError(t, r.Update(ctxb(), Order{ID: "B-2", Total: 9}))
		require.Equal(t, "B-2", o.ID)
		require.Equal(t, 9, o.Total)
	})

	t.Run("Update merges a map (cross-tier)", func(t *testing.T) {
		o, r := wrapOrder(t)

		// the shape a values.Record source's Get produces
		src := values.MustRecord(values.F("total", values.NewVariable(42)))
		m, ok := src.Get(ctxb()).(map[string]any)
		require.True(t, ok)

		require.NoError(t, r.Update(ctxb(), m))
		require.Equal(t, 42, o.Total)
		require.Equal(t, "A-1", o.ID) // untouched fields keep their values
	})

	t.Run("Update map merge — unknown name and wrong shape", func(t *testing.T) {
		_, r := wrapOrder(t)

		require.ErrorContains(t,
			r.Update(ctxb(), map[string]any{"nope": 1}), "no field")
		require.ErrorContains(t, r.Update(ctxb(), 42), "expects")
	})

	t.Run("Update map merge recurses into a sub-record", func(t *testing.T) {
		o, r := wrapOrder(t)

		require.NoError(t, r.Update(ctxb(),
			map[string]any{"ship": map[string]any{"zone": "Z-1"}}))
		require.Equal(t, "Z-1", o.Ship.Zone)
	})
}
