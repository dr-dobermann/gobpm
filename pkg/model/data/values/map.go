package values

import (
	"context"
	"maps"
	"reflect"
	"slices"
	"sync"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

// Map is the generic dynamic map value — the concrete of the fourth
// structural kind (ADR-011 v.7 §2.9.7, SRD-047 FR-2): homogeneous T values
// under non-empty string data keys. It mirrors Array[T] — homogeneity is
// enforced by the type parameter, Map[any] is the zero-setup dictionary for
// engine-assembled data — and, unlike Collection, carries no iteration
// cursor: sorted Keys plus Entry is the complete, stateless enumeration.
type Map[T any] struct {
	entries map[string]T
	lock    sync.Mutex
}

// NewMap creates a Map of T from entries, copying the input (nil is allowed —
// an empty map). An empty-string key in the input is a classified error — map
// keys are non-empty arbitrary strings (SRD-047 §4.2).
func NewMap[T any](entries map[string]T) (*Map[T], error) {
	m := Map[T]{entries: make(map[string]T, len(entries))}

	for k, v := range entries {
		if k == "" {
			return nil, emptyKeyErr("NewMap")
		}

		m.entries[k] = v
	}

	return &m, nil
}

// MustMap is NewMap that panics on error — for tests and static process
// construction.
func MustMap[T any](entries map[string]T) *Map[T] {
	m, err := NewMap(entries)
	if err != nil {
		errs.Panic(err)
	}

	return m
}

// Get returns the whole map as a plain-Go snapshot: a map[string]T copy —
// safe to read and mutate.
func (m *Map[T]) Get(_ context.Context) any {
	m.lock.Lock()
	defer m.lock.Unlock()

	return maps.Clone(m.entries)
}

// Update REPLACES the whole entry set with the given map[string]T (ADR-011
// v.7 §2.9.7 — replace, not merge, so Get/Update round-trip and a replace can
// express deletion; per-entry surgery is SetEntry/DeleteEntry). Any other
// payload shape and an empty-string key are classified errors.
func (m *Map[T]) Update(_ context.Context, value any) error {
	entries, err := checkValue[map[string]T](value)
	if err != nil {
		return err
	}

	fresh := make(map[string]T, len(entries))

	for k, v := range entries {
		if k == "" {
			return emptyKeyErr("Update")
		}

		fresh[k] = v
	}

	m.lock.Lock()
	defer m.lock.Unlock()

	m.entries = fresh

	return nil
}

// Lock locks the Map's internal mutex.
func (m *Map[T]) Lock() { m.lock.Lock() }

// Unlock unlocks the Map's internal mutex.
func (m *Map[T]) Unlock() { m.lock.Unlock() }

// Type returns the name of the Map's element type (the Array[T] convention).
func (m *Map[T]) Type() string {
	return reflect.TypeFor[T]().Name()
}

// Clone creates a clone of the Map with a copy of its entries (the Array[T]
// element-copy contract).
func (m *Map[T]) Clone() data.Value {
	m.lock.Lock()
	defer m.lock.Unlock()

	return &Map[T]{entries: maps.Clone(m.entries)}
}

// ************************ data.Map capability ********************************

// Keys returns all entry keys in ascending (sorted) order — the deterministic
// enumeration over Go's randomized map iteration (SRD-047 NFR-1).
func (m *Map[T]) Keys() []string {
	m.lock.Lock()
	defer m.lock.Unlock()

	out := make([]string, 0, len(m.entries))
	for k := range m.entries {
		out = append(out, k)
	}

	slices.Sort(out)

	return out
}

// Entry returns the value stored under key, or a classified ObjectNotFound
// error when the entry is absent.
func (m *Map[T]) Entry(_ context.Context, key string) (any, error) {
	m.lock.Lock()
	defer m.lock.Unlock()

	v, ok := m.entries[key]
	if !ok {
		return nil, errs.New(
			errs.M("map has no entry %q", key),
			errs.C(errorClass, errs.ObjectNotFound))
	}

	return v, nil
}

// SetEntry upserts the entry under key — permissive on the key (keys are
// data), owner-enforced on the value (the checkValue[T] contract Array[T]
// shares). An empty key is a classified error.
func (m *Map[T]) SetEntry(_ context.Context, key string, value any) error {
	if key == "" {
		return emptyKeyErr("SetEntry")
	}

	v, err := checkValue[T](value)
	if err != nil {
		return err
	}

	m.lock.Lock()
	defer m.lock.Unlock()

	m.entries[key] = v

	return nil
}

// DeleteEntry removes the entry under key, or returns a classified
// ObjectNotFound error when it is absent (fail-loud, like Entry).
func (m *Map[T]) DeleteEntry(_ context.Context, key string) error {
	m.lock.Lock()
	defer m.lock.Unlock()

	if _, ok := m.entries[key]; !ok {
		return errs.New(
			errs.M("map has no entry %q to delete", key),
			errs.C(errorClass, errs.ObjectNotFound))
	}

	delete(m.entries, key)

	return nil
}

// emptyKeyErr builds the classified error for an empty map key: map keys are
// non-empty arbitrary strings (SRD-047 §4.2), and no seam silently
// normalizes or skips a violating one.
func emptyKeyErr(op string) error {
	return errs.New(
		errs.M("%s: an empty map key isn't allowed", op),
		errs.C(errorClass, errs.EmptyNotAllowed))
}

// *****************************************************************************
// check implementation of the data.Value and data.Map interfaces.
var (
	mapInterfaceChecker *Map[int]
	_                   data.Value = mapInterfaceChecker
	_                   data.Map   = mapInterfaceChecker
)
