package adapters

import (
	"context"
	"reflect"
	"slices"
	"sync"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

// mapValue is the live data.Map view over a string-keyed Go map field: reads
// and whole-entry writes go through the addressable field, so the host's map
// IS the dictionary (SRD-047 §4.8, FR-8). Scalar/leaf and passthrough entries
// are served live; a composite entry (struct/slice/nested map) is a
// read-navigable frozen snapshot, since a Go map value is not addressable and
// so cannot carry a deep write-through (freeze, frozen.go). The mutex is the
// root wrapper's, shared by every view into the same wrapped struct.
type mapValue struct {
	mu   *sync.Mutex
	m    reflect.Value // the live, settable map field (may be a nil map)
	typ  string
	elem fieldInfo
}

// newMapValue builds the view over an addressable, string-keyed map field.
func newMapValue(fld reflect.Value, mu *sync.Mutex) *mapValue {
	return &mapValue{
		m:    fld,
		elem: classifyType(fld.Type().Elem()),
		mu:   mu,
		typ:  fld.Type().String(),
	}
}

// keyValue converts a string key to the map's key type — handling named string
// types (type Code string) by kind, matching how the engine treats named types.
func (mv *mapValue) keyValue(key string) reflect.Value {
	return reflect.ValueOf(key).Convert(mv.m.Type().Key())
}

// entryView materializes a map value per the resolved element kind: the held
// data.Value itself (passthrough), a frozen read-navigable view for a composite
// (struct/slice/nested map/custom — non-addressable, §4.8), or the raw value
// for a scalar leaf.
func (mv *mapValue) entryView(el reflect.Value) (any, error) {
	switch mv.elem.kind {
	case kindPassthrough:
		v, ok := el.Interface().(data.Value)
		if !ok || v == nil {
			return nil, errs.New(
				errs.M("a map entry holds no value"),
				errs.C(errorClass, errs.EmptyNotAllowed))
		}

		return v, nil

	case kindCustom, kindRecord, kindCollection, kindMap:
		view, err := mv.compositeView(el)
		if err != nil {
			return nil, err
		}

		return freeze(view), nil

	default: // kindLeaf — a raw scalar
		return el.Interface(), nil
	}
}

// compositeView builds a live sub-view over an addressable COPY of a
// non-addressable map value (the copy is what freeze then makes read-only).
func (mv *mapValue) compositeView(el reflect.Value) (data.Value, error) {
	box := reflect.New(el.Type()).Elem()
	box.Set(el)

	switch mv.elem.kind {
	case kindCustom:
		ta, _ := customFor(mv.elem.goType)

		return ta.custom(box.Addr()), nil

	case kindCollection:
		return newSliceCollection(box, &sync.Mutex{}), nil

	case kindMap:
		return newMapValue(box, &sync.Mutex{}), nil

	default: // kindRecord
		rec := box
		if mv.elem.isPtr {
			if box.IsNil() {
				return nil, errs.New(
					errs.M("a map entry is a nil %s", box.Type().String()),
					errs.C(errorClass, errs.EmptyNotAllowed))
			}

			rec = box.Elem()
		}

		ta, err := adapterFor(mv.elem.goType)
		if err != nil {
			return nil, err
		}

		return &structRecord{ptr: rec, ta: ta, mu: &sync.Mutex{}}, nil
	}
}

// ******************** data.Value ********************

// Get returns a shallow snapshot copy of the live map (the values.Map.Get
// contract — safe to read; entry mutation goes through SetEntry).
func (mv *mapValue) Get(_ context.Context) any {
	mv.mu.Lock()
	defer mv.mu.Unlock()

	out := reflect.MakeMapWithSize(mv.m.Type(), mv.m.Len())

	iter := mv.m.MapRange()
	for iter.Next() {
		out.SetMapIndex(iter.Key(), iter.Value())
	}

	return out.Interface()
}

// Update replaces the whole map with a value assignable to the map's Go type
// (the ADR-011 v.7 §2.9.7 replace contract). Anything else is a classified
// error.
func (mv *mapValue) Update(ctx context.Context, value any) error {
	rv, err := coerce(ctx, "Update", mv.m.Type(), value)
	if err != nil {
		return err
	}

	mv.mu.Lock()
	defer mv.mu.Unlock()

	mv.m.Set(rv)

	return nil
}

// Lock locks the root mutex shared with every view into the wrapped struct.
func (mv *mapValue) Lock() { mv.mu.Lock() }

// Unlock unlocks the shared root mutex.
func (mv *mapValue) Unlock() { mv.mu.Unlock() }

// Type returns the cached map type name.
func (mv *mapValue) Type() string { return mv.typ }

// Clone returns a DETACHED copy: a fresh map with the entries shallow-copied
// and its own mutex — mutating the clone never touches the live map.
func (mv *mapValue) Clone() data.Value {
	mv.mu.Lock()
	defer mv.mu.Unlock()

	fresh := reflect.New(mv.m.Type()).Elem()
	cp := reflect.MakeMapWithSize(mv.m.Type(), mv.m.Len())

	iter := mv.m.MapRange()
	for iter.Next() {
		cp.SetMapIndex(iter.Key(), iter.Value())
	}

	fresh.Set(cp)

	return &mapValue{m: fresh, elem: mv.elem, mu: &sync.Mutex{}, typ: mv.typ}
}

// ******************** data.Map ********************

// Keys returns the entry keys in ascending (sorted) order — deterministic over
// Go's randomized map iteration (SRD-047 NFR-1).
func (mv *mapValue) Keys() []string {
	mv.mu.Lock()
	defer mv.mu.Unlock()

	out := make([]string, 0, mv.m.Len())

	iter := mv.m.MapRange()
	for iter.Next() {
		out = append(out, iter.Key().String())
	}

	slices.Sort(out)

	return out
}

// Entry returns the value stored under key, or a classified ObjectNotFound
// error when the entry is absent.
func (mv *mapValue) Entry(_ context.Context, key string) (any, error) {
	mv.mu.Lock()
	defer mv.mu.Unlock()

	if !mv.m.IsValid() || mv.m.IsNil() {
		return nil, mv.noEntry(key)
	}

	el := mv.m.MapIndex(mv.keyValue(key))
	if !el.IsValid() {
		return nil, mv.noEntry(key)
	}

	return mv.entryView(el)
}

// SetEntry upserts the whole value under key — live for every element kind (a
// composite value is replaced wholesale, the idiomatic Go copy-modify-restore).
// An empty key is a classified error; a nil map is allocated on first write.
func (mv *mapValue) SetEntry(ctx context.Context, key string, value any) error {
	if key == "" {
		return errs.New(
			errs.M("SetEntry: an empty map key isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	rv, err := coerce(ctx, "SetEntry", mv.m.Type().Elem(), value)
	if err != nil {
		return err
	}

	mv.mu.Lock()
	defer mv.mu.Unlock()

	if mv.m.IsNil() {
		mv.m.Set(reflect.MakeMap(mv.m.Type()))
	}

	mv.m.SetMapIndex(mv.keyValue(key), rv)

	return nil
}

// DeleteEntry removes the entry under key, or returns a classified
// ObjectNotFound error when it is absent (fail-loud, like Entry).
func (mv *mapValue) DeleteEntry(_ context.Context, key string) error {
	mv.mu.Lock()
	defer mv.mu.Unlock()

	kv := mv.keyValue(key)
	if !mv.m.IsValid() || mv.m.IsNil() || !mv.m.MapIndex(kv).IsValid() {
		return mv.noEntry(key)
	}

	mv.m.SetMapIndex(kv, reflect.Value{})

	return nil
}

// noEntry builds the classified absent-entry error.
func (mv *mapValue) noEntry(key string) error {
	return errs.New(
		errs.M("map has no entry %q", key),
		errs.C(errorClass, errs.ObjectNotFound))
}

// compile-time interface checks (the values-package idiom).
var (
	mapValueChecker *mapValue
	_               data.Value = mapValueChecker
	_               data.Map   = mapValueChecker
)
