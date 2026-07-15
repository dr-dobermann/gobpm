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
			return build(ptr.Interface().(*T))
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
		return v.(*typeAdapter), nil
	}

	ta, err := buildAdapter(t)
	if err != nil {
		return nil, err
	}

	actual, _ := adapterCache.LoadOrStore(t, ta)

	return actual.(*typeAdapter), nil
}

// customFor reports the Register-ed factory for t, if any — consulted first
// in the field-kind resolution order (§4.10).
func customFor(t reflect.Type) (*typeAdapter, bool) {
	v, ok := adapterCache.Load(t)
	if !ok {
		return nil, false
	}

	ta := v.(*typeAdapter)

	return ta, ta.custom != nil
}
