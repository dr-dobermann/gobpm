package adapters

import (
	"reflect"
	"sync"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

// adapterCache is the type→adapter registry (reflect.Type → *typeAdapter):
// built once per type, read on every Wrap and field classification — the
// encoding/json type-cache pattern.
var adapterCache sync.Map

// Register installs a custom adapter factory for T, pre-empting the
// reflection builder at Wrap and at field classification — the
// Marshaler-analog extension seam (SRD-045 §4.10): it lifts types the host
// cannot modify (third-party structs, time.Time, map types) into
// navigability. The factory receives the live *T and returns the data.Value
// view the engine navigates. Registration is init-time by convention; a
// later Register replaces the cache entry for future wraps only.
func Register[T any](build func(v *T) data.Value) error {
	if build == nil {
		return errs.New(
			errs.M("Register: a nil build factory isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	t := reflect.TypeFor[T]()

	adapterCache.Store(t, &typeAdapter{
		goType: t,
		name:   t.String(),
		custom: func(ptr reflect.Value) data.Value {
			// The cache keys this factory by reflect.TypeFor[T](), so the only
			// pointer that reaches it is a *T. A different type means the
			// resolution order handed the wrong entry to the wrong factory —
			// a broken registry, not bad user input.
			v, ok := ptr.Interface().(*T)
			if !ok {
				errs.Panic(errs.New(
					errs.M("adapters: custom factory for %s got %s",
						t, ptr.Type()),
					errs.C(errorClass, errs.TypeCastingError)))
			}

			return build(v)
		},
	})

	return nil
}

// adapterFor resolves the adapter for t: a cache hit (built or Register-ed)
// wins; a miss runs the reflection builder once and publishes the result.
// LoadOrStore keeps concurrent first-wraps of one type consistent — one
// winner, identical content either way.
func adapterFor(t reflect.Type) (*typeAdapter, error) {
	if v, ok := adapterCache.Load(t); ok {
		return asAdapter(t, v)
	}

	ta, err := buildAdapter(t)
	if err != nil {
		return nil, err
	}

	actual, _ := adapterCache.LoadOrStore(t, ta)

	return asAdapter(t, actual)
}

// asAdapter reads a cache entry. Only adapterFor and Register ever write the
// cache and both store a *typeAdapter, so a foreign value names a corrupted
// registry rather than a caller mistake — reported, not asserted, and checked
// in one place so both readers stay single expressions.
func asAdapter(t reflect.Type, v any) (*typeAdapter, error) {
	ta, ok := v.(*typeAdapter)
	if !ok {
		return nil, errs.Invariant("cache entry for %s is %T, not *typeAdapter", t, v)
	}

	return ta, nil
}

// customFor reports the Register-ed factory for t, if any — consulted first
// in the field-kind resolution order (§4.10).
func customFor(t reflect.Type) (*typeAdapter, bool) {
	v, ok := adapterCache.Load(t)
	if !ok {
		return nil, false
	}

	ta, err := asAdapter(t, v)
	if err != nil {
		return nil, false
	}

	return ta, ta.custom != nil
}
