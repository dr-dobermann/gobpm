package adapters

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

// A composite value read out of a native Go map is NOT addressable
// (reflect.Value.MapIndex never is), so a live write-through sub-view is
// impossible — a deep write would silently mutate a detached copy. freeze
// wraps such a value in a read-navigable, write-erroring snapshot so a deep
// SetPath into a map entry fails loud instead (SRD-047 §4.8, the entry-level
// write contract). The wrapped value is a materialized copy; reads reflect the
// map state at read time, whole-entry writes go through data.Map.SetEntry.
func freeze(v data.Value) data.Value {
	switch inner := v.(type) {
	case data.Record:
		return frozenRecord{inner}
	case data.Collection:
		return frozenCollection{inner}
	case data.Map:
		return frozenMap{inner}
	default:
		return frozenLeaf{inner}
	}
}

// frozenErr is the classified error every frozen mutator returns.
func frozenErr(op string) error {
	return errs.New(
		errs.M("%s: a native map value isn't addressable; re-upsert the "+
			"whole entry via SetEntry", op),
		errs.C(errorClass, errs.InvalidParameter))
}

// ******************** frozenLeaf ********************

// frozenLeaf freezes a scalar/opaque value: reads pass through, Update errors.
type frozenLeaf struct{ v data.Value }

func (f frozenLeaf) Get(ctx context.Context) any { return f.v.Get(ctx) }
func (f frozenLeaf) Update(context.Context, any) error {
	return frozenErr("Update")
}
func (f frozenLeaf) Lock()             { f.v.Lock() }
func (f frozenLeaf) Unlock()           { f.v.Unlock() }
func (f frozenLeaf) Type() string      { return f.v.Type() }
func (f frozenLeaf) Clone() data.Value { return frozenLeaf{f.v.Clone()} }

// ******************** frozenRecord ********************

// frozenRecord freezes a record: reads pass through (each field re-frozen),
// SetField/Update error.
type frozenRecord struct{ r data.Record }

func (f frozenRecord) Get(ctx context.Context) any { return f.r.Get(ctx) }
func (f frozenRecord) Update(context.Context, any) error {
	return frozenErr("Update")
}
func (f frozenRecord) Lock()             { f.r.Lock() }
func (f frozenRecord) Unlock()           { f.r.Unlock() }
func (f frozenRecord) Type() string      { return f.r.Type() }
func (f frozenRecord) Clone() data.Value { return frozenRecord{f.r.Clone().(data.Record)} }

func (f frozenRecord) Keys() []string { return f.r.Keys() }

func (f frozenRecord) Field(ctx context.Context, name string) (data.Value, error) {
	v, err := f.r.Field(ctx, name)
	if err != nil {
		return nil, err
	}

	return freeze(v), nil
}

func (f frozenRecord) SetField(context.Context, string, data.Value) error {
	return frozenErr("SetField")
}

// ******************** frozenCollection ********************

// frozenCollection freezes a collection: reads pass through (each element
// re-frozen), every mutator errors.
type frozenCollection struct{ c data.Collection }

func (f frozenCollection) Get(ctx context.Context) any { return f.c.Get(ctx) }
func (f frozenCollection) Update(context.Context, any) error {
	return frozenErr("Update")
}
func (f frozenCollection) Lock()        { f.c.Lock() }
func (f frozenCollection) Unlock()      { f.c.Unlock() }
func (f frozenCollection) Type() string { return f.c.Type() }
func (f frozenCollection) Clone() data.Value {
	return frozenCollection{f.c.Clone().(data.Collection)}
}

func (f frozenCollection) Count() int                      { return f.c.Count() }
func (f frozenCollection) Rewind()                         { f.c.Rewind() }
func (f frozenCollection) GoTo(p any) error                { return f.c.GoTo(p) }
func (f frozenCollection) Next(d data.StepDirection) error { return f.c.Next(d) }
func (f frozenCollection) GetKeys() []any                  { return f.c.GetKeys() }
func (f frozenCollection) Index() any                      { return f.c.Index() }

func (f frozenCollection) GetAll(ctx context.Context) []any {
	raw := f.c.GetAll(ctx)
	out := make([]any, len(raw))

	for i, e := range raw {
		out[i] = freezeAny(e)
	}

	return out
}

func (f frozenCollection) GetAt(ctx context.Context, index any) (any, error) {
	v, err := f.c.GetAt(ctx, index)
	if err != nil {
		return nil, err
	}

	return freezeAny(v), nil
}

func (f frozenCollection) Clear()                         { /* no-op: frozen */ }
func (f frozenCollection) Add(context.Context, any) error { return frozenErr("Add") }
func (f frozenCollection) SetAt(context.Context, any, any) error {
	return frozenErr("SetAt")
}
func (f frozenCollection) Insert(context.Context, any, any) error {
	return frozenErr("Insert")
}
func (f frozenCollection) Delete(context.Context, any) error {
	return frozenErr("Delete")
}

// ******************** frozenMap ********************

// frozenMap freezes a nested native map value: reads pass through (each entry
// re-frozen), SetEntry/DeleteEntry error — a nested native map is itself a
// non-addressable snapshot, so its whole-entry writes cannot land either.
type frozenMap struct{ m data.Map }

func (f frozenMap) Get(ctx context.Context) any { return f.m.Get(ctx) }
func (f frozenMap) Update(context.Context, any) error {
	return frozenErr("Update")
}
func (f frozenMap) Lock()             { f.m.Lock() }
func (f frozenMap) Unlock()           { f.m.Unlock() }
func (f frozenMap) Type() string      { return f.m.Type() }
func (f frozenMap) Clone() data.Value { return frozenMap{f.m.Clone().(data.Map)} }

func (f frozenMap) Keys() []string { return f.m.Keys() }

func (f frozenMap) Entry(ctx context.Context, key string) (any, error) {
	v, err := f.m.Entry(ctx, key)
	if err != nil {
		return nil, err
	}

	return freezeAny(v), nil
}

func (f frozenMap) SetEntry(context.Context, string, any) error {
	return frozenErr("SetEntry")
}
func (f frozenMap) DeleteEntry(context.Context, string) error {
	return frozenErr("DeleteEntry")
}

// freezeAny freezes a value that may be a data.Value (a sub-view) or a raw Go
// scalar (a leaf element): a raw scalar is already immutable to the caller and
// passes through, a Value is frozen.
func freezeAny(e any) any {
	if v, ok := e.(data.Value); ok {
		return freeze(v)
	}

	return e
}

// compile-time interface checks (the values-package idiom).
var (
	_ data.Value      = frozenLeaf{}
	_ data.Record     = frozenRecord{}
	_ data.Collection = frozenCollection{}
	_ data.Map        = frozenMap{}
)
