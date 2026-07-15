package adapters

import (
	"context"
	"reflect"
	"sync"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

// fieldLeaf is the writable scalar-leaf view of a non-navigable field
// (SRD-045 §4.5 — the opaque default): whole-value reads and type-checked
// writes through the live field, no structural navigation. Unlike the S1
// read-only scalarLeaf, a fieldLeaf is a live, writable window.
type fieldLeaf struct {
	fld reflect.Value
	mu  *sync.Mutex
	typ string
}

// Get returns a copy of the field's value.
func (l *fieldLeaf) Get(_ context.Context) any {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.fld.Interface()
}

// Update writes value through to the live field (type-checked).
func (l *fieldLeaf) Update(ctx context.Context, value any) error {
	rv, err := coerce(ctx, "Update", l.fld.Type(), value)
	if err != nil {
		return err
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	l.fld.Set(rv)

	return nil
}

// Lock locks the root mutex shared with every view into the wrapped struct.
func (l *fieldLeaf) Lock() { l.mu.Lock() }

// Unlock unlocks the shared root mutex.
func (l *fieldLeaf) Unlock() { l.mu.Unlock() }

// Type returns the cached field type name.
func (l *fieldLeaf) Type() string { return l.typ }

// Clone returns a detached leaf holding a copy of the field's value, with
// its own mutex — independent of the live struct.
func (l *fieldLeaf) Clone() data.Value {
	l.mu.Lock()
	defer l.mu.Unlock()

	cp := reflect.New(l.fld.Type()).Elem()
	cp.Set(l.fld)

	return &fieldLeaf{fld: cp, mu: &sync.Mutex{}, typ: l.typ}
}

// compile-time interface check (the values-package idiom).
var (
	fieldLeafChecker *fieldLeaf
	_                data.Value = fieldLeafChecker
)
