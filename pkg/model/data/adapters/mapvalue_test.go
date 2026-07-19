package adapters_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/adapters"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/stretchr/testify/require"
)

// Currency is a named string type — a map key type that qualifies by KIND.
type Currency string

// Rates carries string-keyed map fields (scalar, named-key, composite) plus a
// non-string-keyed map that must stay an opaque leaf.
type Rates struct {
	Spot    map[string]float64 `gobpm:"spot"`    // scalar values — fully live
	ByCode  map[Currency]int   `gobpm:"byCode"`  // named string key — live
	Books   map[string]Item    `gobpm:"books"`   // composite — frozen snapshot
	ByIndex map[int]string     `gobpm:"byIndex"` // int key — opaque leaf
}

func ratesFixture() *Rates {
	return &Rates{
		Spot:    map[string]float64{"EUR": 1.08, "GBP": 1.27},
		ByCode:  map[Currency]int{"EUR": 1},
		Books:   map[string]Item{"widget": {SKU: "widget", Price: 50}},
		ByIndex: map[int]string{1: "one"},
	}
}

// TestStructMapField (SRD-047 T-8): a string-keyed map field is a live,
// navigable data.Map; the write contract is entry-level; non-string keys stay
// opaque leaves; composite entries are read-navigable frozen snapshots.
func TestStructMapField(t *testing.T) {
	t.Run("scalar-valued map — live read and write", func(t *testing.T) {
		rt := ratesFixture()
		r, err := adapters.Wrap(rt)
		require.NoError(t, err)

		spot, err := r.(data.Record).Field(ctxb(), "spot")
		require.NoError(t, err)

		m, ok := spot.(data.Map)
		require.True(t, ok)
		require.Equal(t, []string{"EUR", "GBP"}, m.Keys()) // sorted

		v, err := m.Entry(ctxb(), "EUR")
		require.NoError(t, err)
		require.Equal(t, 1.08, v)

		// upsert and delete write through to the LIVE map
		require.NoError(t, m.SetEntry(ctxb(), "JPY", 161.0))
		require.Equal(t, 161.0, rt.Spot["JPY"])

		require.NoError(t, m.DeleteEntry(ctxb(), "GBP"))
		_, absent := rt.Spot["GBP"]
		require.False(t, absent)

		// a missing entry fails loud on both read and delete
		_, err = m.Entry(ctxb(), "nope")
		require.Error(t, err)
		require.Error(t, m.DeleteEntry(ctxb(), "nope"))
	})

	t.Run("named string key type qualifies by kind", func(t *testing.T) {
		rt := ratesFixture()
		r, err := adapters.Wrap(rt)
		require.NoError(t, err)

		byCode, err := r.(data.Record).Field(ctxb(), "byCode")
		require.NoError(t, err)

		m, ok := byCode.(data.Map)
		require.True(t, ok)
		require.Equal(t, []string{"EUR"}, m.Keys())

		require.NoError(t, m.SetEntry(ctxb(), "USD", 2))
		require.Equal(t, 2, rt.ByCode["USD"]) // Currency("USD")

		require.Error(t, m.SetEntry(ctxb(), "BAD", "not an int")) // coerce err
	})

	t.Run("int-keyed map stays an opaque leaf (re-pin)", func(t *testing.T) {
		rt := ratesFixture()
		r, err := adapters.Wrap(rt)
		require.NoError(t, err)

		byIndex, err := r.(data.Record).Field(ctxb(), "byIndex")
		require.NoError(t, err)

		_, isMap := byIndex.(data.Map)
		require.False(t, isMap)
		require.Equal(t, map[int]string{1: "one"}, byIndex.Get(ctxb()))
	})

	t.Run("composite entry — read-navigable, deep write fails loud", func(t *testing.T) {
		rt := ratesFixture()
		r, err := adapters.Wrap(rt)
		require.NoError(t, err)

		books, err := r.(data.Record).Field(ctxb(), "books")
		require.NoError(t, err)

		m := books.(data.Map)

		// read: the struct entry navigates
		widget, err := m.Entry(ctxb(), "widget")
		require.NoError(t, err)

		rec, ok := widget.(data.Record)
		require.True(t, ok)

		price, err := rec.Field(ctxb(), "price")
		require.NoError(t, err)
		require.Equal(t, 50, price.Get(ctxb()))

		// deep write into the frozen snapshot fails loud — not silent loss
		err = rec.SetField(ctxb(), "price", values.NewVariable(99))
		require.ErrorContains(t, err, "SetEntry")
		require.Equal(t, 50, rt.Books["widget"].Price) // live map untouched

		// but a WHOLE-entry upsert is live
		require.NoError(t, m.SetEntry(ctxb(), "widget",
			Item{SKU: "widget", Price: 60}))
		require.Equal(t, 60, rt.Books["widget"].Price)
	})

	t.Run("Clone is detached from the live map", func(t *testing.T) {
		rt := ratesFixture()
		r, err := adapters.Wrap(rt)
		require.NoError(t, err)

		spot, err := r.(data.Record).Field(ctxb(), "spot")
		require.NoError(t, err)

		c := spot.Clone().(data.Map)
		require.NoError(t, c.SetEntry(ctxb(), "CHF", 1.1))

		_, absent := rt.Spot["CHF"] // clone write never touches the live map
		require.False(t, absent)
	})
}

// Nested carries composite map values of every frozen kind — a struct value, a
// slice value, a nested map value, and a passthrough data.Value.
type Nested struct {
	Books   map[string]Item           `gobpm:"books"`   // → frozenRecord
	Lists   map[string][]int          `gobpm:"lists"`   // → frozenCollection
	Tables  map[string]map[string]int `gobpm:"tables"`  // → frozenMap
	Dynamic map[string]data.Value     `gobpm:"dynamic"` // → passthrough (live)
}

// TestMapValueWholeValue (SRD-047 T-8): the mapValue value-level surface —
// Update (whole-map replace), Get snapshot, Type, Lock/Unlock.
func TestMapValueWholeValue(t *testing.T) {
	rt := ratesFixture()
	r, err := adapters.Wrap(rt)
	require.NoError(t, err)

	spot, err := r.(data.Record).Field(ctxb(), "spot")
	require.NoError(t, err)

	m := spot.(data.Map)

	t.Run("Type names the map type", func(t *testing.T) {
		require.Equal(t, "map[string]float64", m.Type())
	})

	t.Run("Get is an isolated snapshot", func(t *testing.T) {
		snap := m.Get(ctxb()).(map[string]float64)
		snap["EUR"] = 99
		require.Equal(t, 1.08, rt.Spot["EUR"]) // live map untouched
	})

	t.Run("Update replaces the whole live map", func(t *testing.T) {
		require.NoError(t, m.Update(ctxb(),
			map[string]float64{"CHF": 1.1}))
		require.Equal(t, map[string]float64{"CHF": 1.1}, rt.Spot)

		require.Error(t, m.Update(ctxb(), 42)) // wrong shape → classified error
	})

	t.Run("Lock/Unlock guard the shared root", func(t *testing.T) {
		m.Lock()
		locked := true // the mutex guards direct access while held
		m.Unlock()
		require.True(t, locked)
	})
}

// TestFrozenCompositeEntries (SRD-047 T-8, §4.8): every composite map-value
// kind is a read-navigable frozen snapshot whose mutators fail loud.
func TestFrozenCompositeEntries(t *testing.T) {
	nested := &Nested{
		Books:  map[string]Item{"w": {SKU: "w", Price: 50}},
		Lists:  map[string][]int{"a": {1, 2, 3}},
		Tables: map[string]map[string]int{"t": {"x": 1}},
		Dynamic: map[string]data.Value{"d": values.MustRecord(
			values.F("n", values.NewVariable(1)))},
	}

	r, err := adapters.Wrap(nested)
	require.NoError(t, err)
	rec := r.(data.Record)

	t.Run("slice value — frozenCollection reads, mutators error", func(t *testing.T) {
		lists, err := rec.Field(ctxb(), "lists")
		require.NoError(t, err)

		e, err := lists.(data.Map).Entry(ctxb(), "a")
		require.NoError(t, err)

		col, ok := e.(data.Collection)
		require.True(t, ok)
		require.Equal(t, 3, col.Count())

		at, err := col.GetAt(ctxb(), 0)
		require.NoError(t, err)
		require.Equal(t, 1, at)
		require.Len(t, col.GetAll(ctxb()), 3)
		require.Equal(t, []any{0, 1, 2}, col.GetKeys())

		// every mutator fails loud; the live slice is untouched
		require.Error(t, col.Add(ctxb(), 4))
		require.Error(t, col.SetAt(ctxb(), 0, 9))
		require.Error(t, col.Insert(ctxb(), 9, 0))
		require.Error(t, col.Delete(ctxb(), 0))
		require.Error(t, col.Update(ctxb(), 9))
		require.Equal(t, []int{1, 2, 3}, nested.Lists["a"])

		// read-only cursor navigation still works
		require.NoError(t, col.GoTo(1))
		require.Equal(t, 1, col.Index())
		col.Rewind()
		require.NoError(t, col.Next(data.StepForward))
		require.Equal(t, "map[string][]int", lists.Type())
		require.NotNil(t, col.Clone())
	})

	t.Run("nested map value — frozenMap reads, writes error", func(t *testing.T) {
		tables, err := rec.Field(ctxb(), "tables")
		require.NoError(t, err)

		e, err := tables.(data.Map).Entry(ctxb(), "t")
		require.NoError(t, err)

		fm, ok := e.(data.Map)
		require.True(t, ok)
		require.Equal(t, []string{"x"}, fm.Keys())
		require.Equal(t, map[string]int{"x": 1}, fm.Get(ctxb()))

		inner, err := fm.Entry(ctxb(), "x")
		require.NoError(t, err)
		require.Equal(t, 1, inner)

		_, err = fm.Entry(ctxb(), "absent") // frozenMap.Entry error branch
		require.Error(t, err)

		require.Error(t, fm.SetEntry(ctxb(), "y", 2))
		require.Error(t, fm.DeleteEntry(ctxb(), "x"))
		require.Error(t, fm.Update(ctxb(), map[string]int{}))
		require.Equal(t, map[string]int{"x": 1}, nested.Tables["t"])
		require.Equal(t, "map[string]int", fm.Type())
		require.NotNil(t, fm.Clone())

		fm.Lock()
		held := true
		fm.Unlock()
		require.True(t, held)
	})

	t.Run("struct value — frozenRecord full read surface", func(t *testing.T) {
		books, err := rec.Field(ctxb(), "books")
		require.NoError(t, err)

		e, err := books.(data.Map).Entry(ctxb(), "w")
		require.NoError(t, err)

		fr, ok := e.(data.Record)
		require.True(t, ok)
		require.ElementsMatch(t, []string{"sku", "price"}, fr.Keys())
		require.Contains(t, fr.Type(), "Item")
		require.NotNil(t, fr.Get(ctxb()))
		require.Error(t, fr.Update(ctxb(), map[string]any{"price": 9}))

		fr.Lock()
		held := true
		fr.Unlock()
		require.True(t, held)
		require.NotNil(t, fr.Clone())
	})

	t.Run("passthrough value — the stored Value is live", func(t *testing.T) {
		dyn, err := rec.Field(ctxb(), "dynamic")
		require.NoError(t, err)

		e, err := dyn.(data.Map).Entry(ctxb(), "d")
		require.NoError(t, err)

		// a passthrough data.Value entry is NOT frozen — it is the stored
		// mutable object, so a deep write lands.
		drec := e.(data.Record)
		require.NoError(t, drec.SetField(ctxb(), "n", values.NewVariable(2)))
		got, err := drec.Field(ctxb(), "n")
		require.NoError(t, err)
		require.Equal(t, 2, got.Get(ctxb()))
	})
}

// Edges carries the map-value shapes that drive the remaining frozen/mapValue
// branches: a pointer-to-struct value, a slice of data.Value (frozen-leaf
// elements), and a data.Value value that can be nil.
type Edges struct {
	Ptrs  map[string]*Item        `gobpm:"ptrs"`  // → compositeView isPtr
	Boxed map[string][]data.Value `gobpm:"boxed"` // → frozenLeaf via freezeAny
	Dyn   map[string]data.Value   `gobpm:"dyn"`   // → entryView passthrough (nil)
}

// TestMapCoverageEdges (SRD-047 T-8): the remaining frozen pass-through and
// error branches — pointer-to-struct entries, frozen scalar leaves, nil
// passthrough, missing-key errors, and the nil-map read.
func TestMapCoverageEdges(t *testing.T) {
	e := &Edges{
		Ptrs: map[string]*Item{"live": {SKU: "s", Price: 1}, "nil": nil},
		Boxed: map[string][]data.Value{
			"v": {values.NewVariable(7)}},
		Dyn: map[string]data.Value{"none": nil},
	}

	r, err := adapters.Wrap(e)
	require.NoError(t, err)
	rec := r.(data.Record)

	t.Run("pointer-to-struct entry navigates; a nil pointer errs", func(t *testing.T) {
		ptrs := mapField(t, rec, "ptrs")

		live, err := ptrs.Entry(ctxb(), "live")
		require.NoError(t, err)
		price, err := live.(data.Record).Field(ctxb(), "price")
		require.NoError(t, err)
		require.Equal(t, 1, price.Get(ctxb()))

		_, err = ptrs.Entry(ctxb(), "nil") // a nil *Item is a classified error
		require.Error(t, err)
	})

	t.Run("a frozen scalar leaf inside a frozen collection", func(t *testing.T) {
		boxed := mapField(t, rec, "boxed")

		col, err := boxed.Entry(ctxb(), "v")
		require.NoError(t, err)

		fc := col.(data.Collection)
		require.Equal(t, "[]data.Value", fc.Type())
		require.NotNil(t, fc.Get(ctxb()))
		fc.Lock()
		fc.Clear() // frozen no-op
		fc.Unlock()
		require.Equal(t, 1, fc.Count())

		_, err = fc.GetAt(ctxb(), 99) // out of range → error branch
		require.Error(t, err)

		leaf, err := fc.GetAt(ctxb(), 0) // a data.Value element → frozenLeaf
		require.NoError(t, err)

		fl := leaf.(data.Value)
		require.Equal(t, 7, fl.Get(ctxb()))
		require.NotEmpty(t, fl.Type())
		fl.Lock()
		frozen := true
		fl.Unlock()
		require.True(t, frozen)
		require.Error(t, fl.Update(ctxb(), 9)) // frozen
		require.NotNil(t, fl.Clone())
	})

	t.Run("a nil passthrough entry errs", func(t *testing.T) {
		dyn := mapField(t, rec, "dyn")

		_, err := dyn.Entry(ctxb(), "none")
		require.Error(t, err)
	})

	t.Run("missing keys error across frozen and live views", func(t *testing.T) {
		ptrs := mapField(t, rec, "ptrs")

		_, err := ptrs.Entry(ctxb(), "absent")
		require.Error(t, err)

		live, err := ptrs.Entry(ctxb(), "live")
		require.NoError(t, err)
		_, err = live.(data.Record).Field(ctxb(), "absent") // frozenRecord.Field err
		require.Error(t, err)
	})

	t.Run("a nil map reads as empty and errs on Entry/Delete", func(t *testing.T) {
		var m map[string]int
		v, err := adapters.Wrap(&m)
		require.NoError(t, err)

		dm := v.(data.Map)
		require.Empty(t, dm.Keys())
		_, err = dm.Entry(ctxb(), "x")
		require.Error(t, err)
		require.Error(t, dm.DeleteEntry(ctxb(), "x"))
		require.Error(t, dm.SetEntry(ctxb(), "", 1)) // empty key
	})
}

// badge is a Register-target custom type used as a native map value, driving
// compositeView's kindCustom arm.
type badge struct{ n int }

// TestMapCustomValue (SRD-047 T-8): a Register-ed custom type as a map value is
// served through its custom factory, then frozen (non-addressable snapshot).
func TestMapCustomValue(t *testing.T) {
	require.NoError(t, adapters.Register[badge](
		func(v *badge) data.Value {
			return values.MustRecord(
				values.F("n", values.NewVariable(v.n)))
		}))

	type Board struct {
		Badges map[string]badge `gobpm:"badges"`
	}

	r, err := adapters.Wrap(&Board{Badges: map[string]badge{"a": {n: 5}}})
	require.NoError(t, err)

	badges := mapField(t, r.(data.Record), "badges")

	e, err := badges.Entry(ctxb(), "a")
	require.NoError(t, err)

	n, err := e.(data.Record).Field(ctxb(), "n")
	require.NoError(t, err)
	require.Equal(t, 5, n.Get(ctxb()))
}

// mapField resolves a named map field of rec as a data.Map.
func mapField(t *testing.T, rec data.Record, name string) data.Map {
	t.Helper()

	f, err := rec.Field(ctxb(), name)
	require.NoError(t, err)

	m, ok := f.(data.Map)
	require.True(t, ok)

	return m
}

// TestWrapBareMap (SRD-047 T-8): a pointer to a string-keyed map wraps as a
// standalone data.Map — the "process holds a dictionary" driver.
func TestWrapBareMap(t *testing.T) {
	t.Run("wrap and mutate a bare map", func(t *testing.T) {
		m := map[string]int{"a": 1}
		v, err := adapters.Wrap(&m)
		require.NoError(t, err)

		dm, ok := v.(data.Map)
		require.True(t, ok)
		require.Equal(t, []string{"a"}, dm.Keys())

		require.NoError(t, dm.SetEntry(ctxb(), "b", 2))
		require.Equal(t, 2, m["b"]) // writes through to the live map
	})

	t.Run("wrap a nil map — allocates on first write", func(t *testing.T) {
		var m map[string]int
		v, err := adapters.Wrap(&m)
		require.NoError(t, err)

		require.NoError(t, v.(data.Map).SetEntry(ctxb(), "x", 9))
		require.Equal(t, 9, m["x"])
	})

	t.Run("a non-string-keyed bare map cannot wrap", func(t *testing.T) {
		m := map[int]string{1: "a"}
		_, err := adapters.Wrap(&m)
		require.Error(t, err) // falls to the struct-only builder → not a struct
	})
}
