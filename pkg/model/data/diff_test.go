package data_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/stretchr/testify/require"
)

// orderV builds {total, items:[{price}...], tags:[...string]} — the diff
// fixture (the §3.1a worked-example shape).
func orderV(total int, prices []int, tags ...string) data.Value {
	items := make([]data.Value, len(prices))
	for i, p := range prices {
		items[i] = values.MustRecord(values.F("price", values.NewVariable(p)))
	}

	return values.MustRecord(
		values.F("total", values.NewVariable(total)),
		values.F("items", values.NewArray(items...)),
		values.F("tags", values.NewArray(tags...)),
	)
}

// fxRec wraps a float64 map as {fx: <map>} — the map diff fixture.
func fxRec(entries map[string]float64) data.Value {
	return values.MustRecord(values.F("fx", values.MustMap(entries)))
}

// mRec wraps a Value map as {m: <map>} — for nested/escaped-key map cases.
func mRec(entries map[string]data.Value) data.Value {
	return values.MustRecord(values.F("m", values.MustMap(entries)))
}

// TestDiffValues covers every §3.1 recursion rule of the commit-diff
// (SRD-044 T-1) plus the map kind (SRD-047 T-5).
func TestDiffValues(t *testing.T) {
	tests := []struct {
		name string
		old  data.Value
		new  data.Value
		want []data.Change
	}{
		{"both nil", nil, nil, nil},
		{"added root — one change, no per-leaf explosion",
			nil, orderV(100, []int{50}),
			[]data.Change{{Path: "order", Type: data.ValueAdded}}},
		{"deleted root",
			orderV(100, []int{50}), nil,
			[]data.Change{{Path: "order", Type: data.ValueDeleted}}},
		{"no change → nil",
			orderV(100, []int{50, 100}, "urgent"),
			orderV(100, []int{50, 100}, "urgent"),
			nil},
		{"scalar leaf updated",
			orderV(100, []int{50}), orderV(150, []int{50}),
			[]data.Change{{Path: "order.total", Type: data.ValueUpdated}}},
		{"worked example §3.1a: update + append",
			orderV(100, []int{50, 100}), orderV(150, []int{50, 100, 20}),
			[]data.Change{
				{Path: "order.total", Type: data.ValueUpdated},
				{Path: "order.items[2]", Type: data.ValueAdded}}},
		{"list element removed (truncation)",
			orderV(100, []int{50, 100}), orderV(100, []int{50}),
			[]data.Change{{Path: "order.items[1]", Type: data.ValueDeleted}}},
		{"nested record-in-list leaf updated",
			orderV(100, []int{50, 100}), orderV(100, []int{50, 90}),
			[]data.Change{
				{Path: "order.items[1].price", Type: data.ValueUpdated}}},
		{"raw scalar collection element updated",
			orderV(100, nil, "urgent"), orderV(100, nil, "bulk"),
			[]data.Change{{Path: "order.tags[0]", Type: data.ValueUpdated}}},
		{"record field added — one change at its root",
			values.MustRecord(values.F("a", values.NewVariable(1))),
			values.MustRecord(
				values.F("a", values.NewVariable(1)),
				values.F("b", values.MustRecord(
					values.F("c", values.NewVariable(2))))),
			[]data.Change{{Path: "order.b", Type: data.ValueAdded}}},
		{"record field removed",
			values.MustRecord(
				values.F("a", values.NewVariable(1)),
				values.F("b", values.NewVariable(2))),
			values.MustRecord(values.F("a", values.NewVariable(1))),
			[]data.Change{{Path: "order.b", Type: data.ValueDeleted}}},
		{"kind change scalar→record — one Updated, no descent",
			values.MustRecord(values.F("a", values.NewVariable(1))),
			values.MustRecord(values.F("a",
				values.MustRecord(values.F("x", values.NewVariable(2))))),
			[]data.Change{{Path: "order.a", Type: data.ValueUpdated}}},
		{"kind change record→list — one Updated, no descent",
			values.MustRecord(values.F("a",
				values.MustRecord(values.F("x", values.NewVariable(1))))),
			values.MustRecord(values.F("a", values.NewArray(1, 2))),
			[]data.Change{{Path: "order.a", Type: data.ValueUpdated}}},
		{"plain scalar roots equal", values.NewVariable(5),
			values.NewVariable(5), nil},
		{"plain scalar roots differ", values.NewVariable(5),
			values.NewVariable(6),
			[]data.Change{{Path: "order", Type: data.ValueUpdated}}},

		// ---- map cases (SRD-047 T-5) ----
		{"map entry updated",
			fxRec(map[string]float64{"EUR": 1.08}),
			fxRec(map[string]float64{"EUR": 1.09}),
			[]data.Change{{Path: `order.fx["EUR"]`, Type: data.ValueUpdated}}},
		{"map entries added and deleted, sorted deterministic order",
			fxRec(map[string]float64{"EUR": 1.08, "GBP": 1.27}),
			fxRec(map[string]float64{"EUR": 1.09, "JPY": 161}),
			[]data.Change{
				// new-side sorted (EUR updated, JPY added), then old-only (GBP)
				{Path: `order.fx["EUR"]`, Type: data.ValueUpdated},
				{Path: `order.fx["JPY"]`, Type: data.ValueAdded},
				{Path: `order.fx["GBP"]`, Type: data.ValueDeleted}}},
		{"whole map added — one change at its root",
			values.MustRecord(values.F("a", values.NewVariable(1))),
			values.MustRecord(
				values.F("a", values.NewVariable(1)),
				values.F("fx", values.MustMap(
					map[string]float64{"EUR": 1.08}))),
			[]data.Change{{Path: "order.fx", Type: data.ValueAdded}}},
		{"record inside a map entry — leaf-granular recursion",
			mRec(map[string]data.Value{
				"spot": values.MustRecord(values.F("bid",
					values.NewVariable(1)))}),
			mRec(map[string]data.Value{
				"spot": values.MustRecord(values.F("bid",
					values.NewVariable(2)))}),
			[]data.Change{
				{Path: `order.m["spot"].bid`, Type: data.ValueUpdated}}},
		{"escaped key in the emitted path",
			mRec(map[string]data.Value{`a"b`: values.NewVariable(1)}),
			mRec(map[string]data.Value{`a"b`: values.NewVariable(2)}),
			[]data.Change{
				{Path: `order.m["a\"b"]`, Type: data.ValueUpdated}}},
		{"kind change map→record — one Updated, no descent",
			values.MustRecord(values.F("a",
				values.MustMap(map[string]float64{"EUR": 1.08}))),
			values.MustRecord(values.F("a",
				values.MustRecord(values.F("x", values.NewVariable(2))))),
			[]data.Change{{Path: "order.a", Type: data.ValueUpdated}}},
		{"kind change map→scalar — one Updated, no descent",
			values.MustRecord(values.F("a",
				values.MustMap(map[string]float64{"EUR": 1.08}))),
			values.MustRecord(values.F("a", values.NewVariable(1))),
			[]data.Change{{Path: "order.a", Type: data.ValueUpdated}}},
		{"unchanged map → nil",
			fxRec(map[string]float64{"EUR": 1.08, "GBP": 1.27}),
			fxRec(map[string]float64{"EUR": 1.08, "GBP": 1.27}),
			nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := data.DiffValues("order", tt.old, tt.new)
			require.Equal(t, tt.want, got)
		})
	}
}
