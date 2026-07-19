package adapters

import (
	"context"
	"reflect"
	"sync"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

// structRecord is the live data.Record view over a wrapped struct: the
// addressable struct value, its cached type adapter, and the root-shared
// mutex every view into the same wrapped struct uses (SRD-045 §4.6).
type structRecord struct {
	ta  *typeAdapter
	mu  *sync.Mutex
	ptr reflect.Value
}

// Keys returns the participating field names in declaration order
// (tag-renamed; gobpm:"-" and unexported fields absent).
func (r *structRecord) Keys() []string {
	out := make([]string, len(r.ta.fields))
	for i, f := range r.ta.fields {
		out[i] = f.name
	}

	return out
}

// Field returns a LIVE view of the named field per its cached kind: a
// sub-record for a struct, a collection for a slice, the held value itself
// for a passthrough (data.Value) field, the Register-ed view for a custom
// type, or a writable leaf for anything else.
func (r *structRecord) Field(_ context.Context, name string) (data.Value, error) {
	fi, err := r.fieldInfo("Field", name)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	fld := r.ptr.Field(fi.index)

	switch fi.kind {
	case kindCustom:
		ta, _ := customFor(fi.goType)

		return ta.custom(fld.Addr()), nil

	case kindPassthrough:
		v, ok := fld.Interface().(data.Value)
		if !ok || v == nil {
			return nil, errs.New(
				errs.M("Field: %q holds no value", name),
				errs.C(errorClass, errs.EmptyNotAllowed))
		}

		return v, nil

	case kindRecord:
		if fi.isPtr {
			if fld.IsNil() {
				return nil, errs.New(
					errs.M("Field: %q is a nil %s", name, fld.Type().String()),
					errs.C(errorClass, errs.EmptyNotAllowed))
			}

			fld = fld.Elem()
		}

		ta, err := adapterFor(fi.goType)
		if err != nil {
			return nil, err
		}

		return &structRecord{ptr: fld, ta: ta, mu: r.mu}, nil

	case kindCollection:
		return newSliceCollection(fld, r.mu), nil

	case kindMap:
		return newMapValue(fld, r.mu), nil

	default: // kindLeaf
		return &fieldLeaf{fld: fld, mu: r.mu, typ: fi.goType.String()}, nil
	}
}

// SetField writes v through to the live struct field, type-checked: v (or its
// Get snapshot) must be assignable to the field's Go type. Unknown names and
// type clashes are classified errors — the typed-target rejection.
func (r *structRecord) SetField(
	ctx context.Context, name string, v data.Value,
) error {
	fi, err := r.fieldInfo("SetField", name)
	if err != nil {
		return err
	}

	if v == nil {
		return errs.New(
			errs.M("SetField: a nil value for %q isn't allowed", name),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	rv, err := coerce(ctx, "SetField", r.ptr.Field(fi.index).Type(), v)
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.ptr.Field(fi.index).Set(rv)

	return nil
}

// Get returns the native Go object — a copy of the dereferenced struct value
// (the SRD-042 §3.2 per-tier contract).
func (r *structRecord) Get(_ context.Context) any {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.ptr.Interface()
}

// Update replaces or merges the wrapped struct: the same struct type replaces
// it whole (through the pointer); a map[string]any merges matching fields via
// the typed setters (the values.Record contract — so cross-tier whole-value
// assignment lands, SRD-045 §4.3). Anything else is a classified error.
func (r *structRecord) Update(ctx context.Context, value any) error {
	if rv := reflect.ValueOf(value); value != nil &&
		rv.Type().AssignableTo(r.ta.goType) {
		r.mu.Lock()
		defer r.mu.Unlock()

		r.ptr.Set(rv)

		return nil
	}

	m, ok := value.(map[string]any)
	if !ok {
		return errs.New(
			errs.M("Update: expects a %s or a map[string]any, got %T",
				r.ta.name, value),
			errs.C(errorClass, errs.TypeCastingError))
	}

	for name, fv := range m {
		if err := r.updateField(ctx, name, fv); err != nil {
			return err
		}
	}

	return nil
}

// updateField merges one map entry: a directly-assignable value sets the
// field; a map into a sub-record recurses through the field view's own
// Update (the values.Record merge shape).
func (r *structRecord) updateField(
	ctx context.Context, name string, fv any,
) error {
	fi, err := r.fieldInfo("Update", name)
	if err != nil {
		return err
	}

	if fv != nil {
		rv := reflect.ValueOf(fv)
		if rv.Type().AssignableTo(r.ptr.Field(fi.index).Type()) {
			r.mu.Lock()
			defer r.mu.Unlock()

			r.ptr.Field(fi.index).Set(rv)

			return nil
		}
	}

	view, err := r.Field(ctx, name)
	if err != nil {
		return err
	}

	return view.Update(ctx, fv)
}

// Lock locks the root mutex shared by every view into this wrapped struct.
func (r *structRecord) Lock() { r.mu.Lock() }

// Unlock unlocks the shared root mutex.
func (r *structRecord) Unlock() { r.mu.Unlock() }

// Type returns the cached Go type name (no per-access reflection).
func (r *structRecord) Type() string { return r.ta.name }

// Clone returns an independent wrapped copy: the struct copied by value,
// slice fields re-backed with a fresh array (elements value-copied), and
// passthrough data.Value fields cloned via their own Clone. Pointer, map,
// and opaque internals stay shared — the documented shallow rule
// (SRD-045 §4.4).
func (r *structRecord) Clone() data.Value {
	r.mu.Lock()
	defer r.mu.Unlock()

	n := reflect.New(r.ta.goType).Elem()
	n.Set(r.ptr)

	for _, fi := range r.ta.fields {
		switch fi.kind {
		case kindCollection:
			old := n.Field(fi.index)
			if old.IsNil() {
				continue
			}

			fresh := reflect.MakeSlice(old.Type(), old.Len(), old.Len())
			reflect.Copy(fresh, old)
			n.Field(fi.index).Set(fresh)

		case kindPassthrough:
			if v, ok := n.Field(fi.index).Interface().(data.Value); ok &&
				v != nil {
				n.Field(fi.index).Set(reflect.ValueOf(v.Clone()))
			}
		}
	}

	return &structRecord{ptr: n, ta: r.ta, mu: &sync.Mutex{}}
}

// fieldInfo resolves a participating field by its process name, or reports a
// classified unknown-name error for op.
func (r *structRecord) fieldInfo(op, name string) (fieldInfo, error) {
	i, ok := r.ta.byName[name]
	if !ok {
		return fieldInfo{}, errs.New(
			errs.M("%s: %s has no field %q", op, r.ta.name, name),
			errs.C(errorClass, errs.ObjectNotFound))
	}

	return r.ta.fields[i], nil
}

// compile-time interface checks (the values-package idiom).
var (
	structRecordChecker *structRecord
	_                   data.Value  = structRecordChecker
	_                   data.Record = structRecordChecker
)
