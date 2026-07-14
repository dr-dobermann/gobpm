package values

import (
	"context"
	"sync"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

// Record is the dynamic, engine-assembled record value — the zero-setup tier
// of ADR-011 v.6 §2.9.5. It is a string-keyed, insertion-ordered, heterogeneous
// set of field Values; it implements data.Value and the data.Record capability,
// so a structural path ("order.items[0].price") navigates into it via Field.
// It is permissive: SetField adds an unknown field (native-struct-backed records
// that reject unknown fields are the S4 adapter tier).
type Record struct {
	fields map[string]data.Value
	order  []string
	lock   sync.Mutex
}

// RecordField is one field of a Record: a name and its value.
type RecordField struct {
	V    data.Value
	Name string
}

// F is a shorthand for a RecordField literal.
func F(name string, v data.Value) RecordField {
	return RecordField{Name: name, V: v}
}

// NewRecord creates a Record from the given fields, in order. A field name must
// be CheckName-legal (so every field is addressable by a structural path) and a
// field value must not be nil; a duplicate name is rejected.
func NewRecord(fields ...RecordField) (*Record, error) {
	r := &Record{
		fields: make(map[string]data.Value, len(fields)),
		order:  make([]string, 0, len(fields)),
	}

	for _, f := range fields {
		if err := data.CheckName(f.Name, errorClass); err != nil {
			return nil, err
		}

		if f.V == nil {
			return nil, errs.New(
				errs.M("record field %q has a nil value", f.Name),
				errs.C(errorClass, errs.EmptyNotAllowed))
		}

		if _, ok := r.fields[f.Name]; ok {
			return nil, errs.New(
				errs.M("duplicate record field %q", f.Name),
				errs.C(errorClass, errs.DuplicateObject))
		}

		r.fields[f.Name] = f.V
		r.order = append(r.order, f.Name)
	}

	return r, nil
}

// MustRecord is NewRecord that panics on error — for tests and static process
// construction.
func MustRecord(fields ...RecordField) *Record {
	r, err := NewRecord(fields...)
	if err != nil {
		errs.Panic(err)
	}

	return r
}

// Get returns the whole record as a deep plain-Go snapshot: a map[string]any
// keyed by field name, each field via its own Value.Get (a nested record yields
// a nested map). The snapshot is a copy — safe to read and mutate.
func (r *Record) Get(ctx context.Context) any {
	r.lock.Lock()
	defer r.lock.Unlock()

	out := make(map[string]any, len(r.order))
	for _, name := range r.order {
		out[name] = r.fields[name].Get(ctx)
	}

	return out
}

// Update replaces matching fields from a map[string]any: each entry whose key is
// an existing field updates that field's Value; unknown keys and a non-map value
// are rejected, so whole-value Update stays shape-preserving.
func (r *Record) Update(ctx context.Context, value any) error {
	m, ok := value.(map[string]any)
	if !ok {
		return errs.New(
			errs.M("record Update expects a map[string]any, got %T", value),
			errs.C(errorClass, errs.TypeCastingError))
	}

	r.lock.Lock()
	defer r.lock.Unlock()

	for name, v := range m {
		f, ok := r.fields[name]
		if !ok {
			return errs.New(
				errs.M("record has no field %q to update", name),
				errs.C(errorClass, errs.ObjectNotFound))
		}

		if err := f.Update(ctx, v); err != nil {
			return err
		}
	}

	return nil
}

// Lock locks the Record's internal mutex.
func (r *Record) Lock() { r.lock.Lock() }

// Unlock unlocks the Record's internal mutex.
func (r *Record) Unlock() { r.lock.Unlock() }

// Type returns the Record's type name.
func (r *Record) Type() string { return "record" }

// Clone deep-clones the Record: a fresh Record with each field's Value.Clone,
// preserving insertion order.
func (r *Record) Clone() data.Value {
	r.lock.Lock()
	defer r.lock.Unlock()

	c := &Record{
		fields: make(map[string]data.Value, len(r.order)),
		order:  make([]string, len(r.order)),
	}

	copy(c.order, r.order)
	for name, v := range r.fields {
		c.fields[name] = v.Clone()
	}

	return c
}

// *********************** data.Record capability ******************************

// Keys returns the field names in insertion order.
func (r *Record) Keys() []string {
	r.lock.Lock()
	defer r.lock.Unlock()

	out := make([]string, len(r.order))
	copy(out, r.order)

	return out
}

// Field returns the named field's value, or a classified ObjectNotFound error
// when the field is absent.
func (r *Record) Field(_ context.Context, name string) (data.Value, error) {
	r.lock.Lock()
	defer r.lock.Unlock()

	v, ok := r.fields[name]
	if !ok {
		return nil, errs.New(
			errs.M("record has no field %q", name),
			errs.C(errorClass, errs.ObjectNotFound))
	}

	return v, nil
}

// SetField adds or replaces the named field. The name must be CheckName-legal
// and the value non-nil; a new name is appended to the field order.
func (r *Record) SetField(_ context.Context, name string, v data.Value) error {
	if err := data.CheckName(name, errorClass); err != nil {
		return err
	}

	if v == nil {
		return errs.New(
			errs.M("record field %q value couldn't be nil", name),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	r.lock.Lock()
	defer r.lock.Unlock()

	if _, ok := r.fields[name]; !ok {
		r.order = append(r.order, name)
	}

	r.fields[name] = v

	return nil
}

// *****************************************************************************
// check implementation of the data.Value and data.Record interfaces
var (
	recordInterfaceChecker *Record
	_                      data.Value  = recordInterfaceChecker
	_                      data.Record = recordInterfaceChecker
)
