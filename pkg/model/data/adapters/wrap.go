package adapters

import (
	"reflect"
	"sync"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

// Wrap wraps a live pointer-to-struct as a navigable data.Value satisfying
// data.Record (ADR-011 v.6 §2.9.5 — wrap, not convert): reads, writes, path
// walks, and diffs go through the LIVE value. The type's adapter is built on
// the first Wrap of that type and cached; a Register-ed custom factory
// pre-empts the builder. A nil, non-pointer, pointer-to-non-struct
// (unregistered), or nil-pointer argument is a classified error.
func Wrap(ptr any) (data.Value, error) {
	if ptr == nil {
		return nil, errs.New(
			errs.M("Wrap: a nil value isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	rv := reflect.ValueOf(ptr)
	if rv.Kind() != reflect.Pointer {
		return nil, errs.New(
			errs.M("Wrap: expects a pointer to struct, got %s",
				rv.Type().String()),
			errs.C(errorClass, errs.TypeCastingError))
	}

	if rv.IsNil() {
		return nil, errs.New(
			errs.M("Wrap: a nil %s isn't allowed", rv.Type().String()),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	ta, err := adapterFor(rv.Type().Elem())
	if err != nil {
		return nil, err
	}

	if ta.custom != nil {
		return ta.custom(rv), nil
	}

	return &structRecord{ptr: rv.Elem(), ta: ta, mu: &sync.Mutex{}}, nil
}

// MustWrap is the panic-on-error twin of Wrap (the values.MustRecord idiom).
func MustWrap(ptr any) data.Value {
	v, err := Wrap(ptr)
	if err != nil {
		errs.Panic(err)

		return nil
	}

	return v
}
