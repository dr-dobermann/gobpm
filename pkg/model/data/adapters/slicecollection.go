package adapters

import (
	"context"
	"io"
	"reflect"
	"sync"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

// sliceCollection is the live data.Collection view over a slice field: reads
// and mutations go through the addressable field, so the host's slice IS the
// collection. The iteration cursor belongs to the view (each Field call hands
// out a fresh one); the mutex is the root wrapper's (SRD-045 §4.6). Element
// access follows the §4.10 resolution order, resolved once at construction —
// struct elements come back as live sub-record views, scalars raw (the
// values.Array precedent).
type sliceCollection struct {
	mu    *sync.Mutex
	slice reflect.Value
	typ   string
	elem  fieldInfo
	index int
}

// newSliceCollection builds the view over an addressable slice field.
func newSliceCollection(fld reflect.Value, mu *sync.Mutex) *sliceCollection {
	idx := -1
	if fld.Len() > 0 {
		idx = 0
	}

	return &sliceCollection{
		slice: fld,
		elem:  classifyType(fld.Type().Elem()),
		mu:    mu,
		typ:   fld.Type().String(),
		index: idx,
	}
}

// elemView materializes element i per the resolved element kind: a
// Register-ed custom view, the held data.Value itself (passthrough), a live
// sub-record for a struct, or the raw value for a scalar leaf.
func (s *sliceCollection) elemView(i int) (any, error) {
	el := s.slice.Index(i)

	switch s.elem.kind {
	case kindCustom:
		ta, _ := customFor(s.elem.goType)

		return ta.custom(el.Addr()), nil

	case kindRecord:
		if s.elem.isPtr {
			if el.IsNil() {
				return nil, errs.New(
					errs.M("element %d is a nil %s", i, el.Type().String()),
					errs.C(errorClass, errs.EmptyNotAllowed))
			}

			el = el.Elem()
		}

		ta, err := adapterFor(s.elem.goType)
		if err != nil {
			return nil, err
		}

		return &structRecord{ptr: el, ta: ta, mu: s.mu}, nil

	default: // kindPassthrough and kindLeaf both surface the raw value
		return el.Interface(), nil
	}
}

// ******************** data.Value ********************

// Get returns the element at the current cursor; an empty collection panics
// (the values.Array contract).
func (s *sliceCollection) Get(_ context.Context) any {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.index < 0 {
		errs.Panic("collection is empty")
	}

	return s.slice.Index(s.index).Interface()
}

// Update replaces the element at the current cursor (type-checked).
func (s *sliceCollection) Update(ctx context.Context, value any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.index < 0 {
		return errs.New(
			errs.M("collection is empty"),
			errs.C(errorClass, errs.EmptyCollectionError))
	}

	rv, err := coerce(ctx, "Update", s.slice.Type().Elem(), value)
	if err != nil {
		return err
	}

	s.slice.Index(s.index).Set(rv)

	return nil
}

// Lock locks the root mutex shared with every view into the wrapped struct.
func (s *sliceCollection) Lock() { s.mu.Lock() }

// Unlock unlocks the shared root mutex.
func (s *sliceCollection) Unlock() { s.mu.Unlock() }

// Type returns the cached slice type name.
func (s *sliceCollection) Type() string { return s.typ }

// Clone returns a DETACHED copy of the collection: a fresh backing array
// (elements value-copied) over its own storage, cursor preserved, its own
// mutex — mutating the clone never touches the live struct.
func (s *sliceCollection) Clone() data.Value {
	s.mu.Lock()
	defer s.mu.Unlock()

	fresh := reflect.New(s.slice.Type()).Elem()
	cp := reflect.MakeSlice(s.slice.Type(), s.slice.Len(), s.slice.Len())
	reflect.Copy(cp, s.slice)
	fresh.Set(cp)

	return &sliceCollection{
		slice: fresh,
		elem:  s.elem,
		mu:    &sync.Mutex{},
		typ:   s.typ,
		index: s.index,
	}
}

// ******************** data.Collection ********************

// Count returns the live slice's length.
func (s *sliceCollection) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.slice.Len()
}

// Rewind sets the cursor to the first element (a no-op when empty).
func (s *sliceCollection) Rewind() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.index < 0 {
		return
	}

	s.index = 0
}

// GoTo sets the cursor to position (an int; negative counts from the end —
// the values.Array contract).
func (s *sliceCollection) GoTo(position any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx, ok := position.(int)
	if !ok {
		return errs.New(
			errs.M("GoTo: position (%v) isn't an int", position),
			errs.C(errorClass, errs.TypeCastingError))
	}

	if idx < 0 {
		idx = s.slice.Len() + idx
	}

	if err := s.checkIndex(idx); err != nil {
		return err
	}

	s.index = idx

	return nil
}

// Next shifts the cursor one step in dir; stepping past the end reports
// io.EOF (the values.Array contract).
func (s *sliceCollection) Next(dir data.StepDirection) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx := s.index
	if dir == data.StepForward {
		idx++
	} else {
		idx--
	}

	if idx == s.slice.Len() {
		return io.EOF
	}

	if err := s.checkIndex(idx); err != nil {
		return err
	}

	s.index = idx

	return nil
}

// GetAll returns every element, struct elements as live sub-record views.
func (s *sliceCollection) GetAll(_ context.Context) []any {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]any, s.slice.Len())
	for i := range s.slice.Len() {
		v, err := s.elemView(i)
		if err != nil {
			v = s.slice.Index(i).Interface()
		}

		out[i] = v
	}

	return out
}

// GetKeys returns the element indexes.
func (s *sliceCollection) GetKeys() []any {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]any, s.slice.Len())
	for i := range s.slice.Len() {
		out[i] = i
	}

	return out
}

// Index returns the current cursor (-1 when empty).
func (s *sliceCollection) Index() any {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.index
}

// Clear empties the live slice and parks the cursor.
func (s *sliceCollection) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.slice.Set(reflect.MakeSlice(s.slice.Type(), 0, 0))
	s.index = -1
}

// Add appends value to the live slice (type-checked), seating the cursor if
// the collection was empty (the values.Array contract).
func (s *sliceCollection) Add(ctx context.Context, value any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rv, err := coerce(ctx, "Add", s.slice.Type().Elem(), value)
	if err != nil {
		return err
	}

	s.slice.Set(reflect.Append(s.slice, rv))

	if s.index < 0 {
		s.index = 0
	}

	return nil
}

// GetAt returns the element at index — a live sub-record view for a struct
// element, the raw value for a scalar.
func (s *sliceCollection) GetAt(_ context.Context, index any) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx, ok := index.(int)
	if !ok {
		return nil, errs.New(
			errs.M("GetAt: index (%v) isn't an int", index),
			errs.C(errorClass, errs.TypeCastingError))
	}

	if err := s.checkIndex(idx); err != nil {
		return nil, err
	}

	return s.elemView(idx)
}

// SetAt sets the element at index — the atomic, cursor-free indexed write
// with the S2 bounds: [0, len) replaces, index == len appends (seating the
// cursor if the collection was empty), past-len errors.
func (s *sliceCollection) SetAt(ctx context.Context, index, value any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx, ok := index.(int)
	if !ok {
		return errs.New(
			errs.M("SetAt: index (%v) isn't an int", index),
			errs.C(errorClass, errs.TypeCastingError))
	}

	rv, err := coerce(ctx, "SetAt", s.slice.Type().Elem(), value)
	if err != nil {
		return err
	}

	switch {
	case idx >= 0 && idx < s.slice.Len():
		s.slice.Index(idx).Set(rv)

	case idx == s.slice.Len():
		s.slice.Set(reflect.Append(s.slice, rv))

		if s.index < 0 {
			s.index = 0
		}

	default:
		return errs.New(
			errs.M("SetAt: index %d is out of range (len: %d)",
				idx, s.slice.Len()),
			errs.C(errorClass, errs.OutOfRangeError))
	}

	return nil
}

// Insert places value at index in [0, len] (type-checked), seating the
// cursor when inserting into an empty collection.
func (s *sliceCollection) Insert(
	ctx context.Context, value, index any,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx, ok := index.(int)
	if !ok {
		return errs.New(
			errs.M("Insert: index (%v) isn't an int", index),
			errs.C(errorClass, errs.TypeCastingError))
	}

	if idx < 0 || idx > s.slice.Len() {
		return errs.New(
			errs.M("Insert: index %d is out of range (len: %d)",
				idx, s.slice.Len()),
			errs.C(errorClass, errs.OutOfRangeError))
	}

	rv, err := coerce(ctx, "Insert", s.slice.Type().Elem(), value)
	if err != nil {
		return err
	}

	wasEmpty := s.slice.Len() == 0

	grown := reflect.Append(s.slice, rv) // grow by one, then shift the tail
	reflect.Copy(grown.Slice(idx+1, grown.Len()), grown.Slice(idx, grown.Len()))
	grown.Index(idx).Set(rv)
	s.slice.Set(grown)

	if wasEmpty {
		s.index = 0
	}

	return nil
}

// Delete removes the element at index, re-seating the cursor so the
// collection stays usable (the values.Array contract).
func (s *sliceCollection) Delete(_ context.Context, index any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx, ok := index.(int)
	if !ok {
		return errs.New(
			errs.M("Delete: index (%v) isn't an int", index),
			errs.C(errorClass, errs.TypeCastingError))
	}

	if err := s.checkIndex(idx); err != nil {
		return err
	}

	head := s.slice.Slice(0, idx)
	tail := s.slice.Slice(idx+1, s.slice.Len())
	s.slice.Set(reflect.AppendSlice(head, tail))

	if s.slice.Len() == 0 {
		s.index = -1
	} else if s.index >= s.slice.Len() {
		s.index = s.slice.Len() - 1
	}

	return nil
}

// checkIndex validates a random-access index against the live length.
func (s *sliceCollection) checkIndex(idx int) error {
	if s.slice.Len() == 0 {
		return errs.New(
			errs.M("collection is empty"),
			errs.C(errorClass, errs.EmptyCollectionError))
	}

	if idx < 0 || idx >= s.slice.Len() {
		return errs.New(
			errs.M("index %d is out of range (max index: %d)",
				idx, s.slice.Len()-1),
			errs.C(errorClass, errs.OutOfRangeError))
	}

	return nil
}

// compile-time interface checks (the values-package idiom).
var (
	sliceCollectionChecker *sliceCollection
	_                      data.Value      = sliceCollectionChecker
	_                      data.Collection = sliceCollectionChecker
)
