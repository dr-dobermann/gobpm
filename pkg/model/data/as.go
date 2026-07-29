package data

import (
	"context"
	"fmt"
	"reflect"

	"github.com/dr-dobermann/gobpm/pkg/errs"
)

// As returns v's payload as T. It rejects a nil Value and, when the payload
// is not a T, reports a self-identifying error naming both the held and the
// requested type — instead of the silent zero value a discarded type
// assertion produces.
func As[T any](ctx context.Context, v Value) (T, error) {
	var zero T

	if v == nil {
		return zero, errs.New(
			errs.M("As: a nil Value isn't allowed"),
			errs.C(errorClass, errs.InvalidParameter))
	}

	payload := v.Get(ctx)

	t, ok := payload.(T)
	if !ok {
		// reflect names the requested type exactly even when T is an
		// interface, whose zero value %T would print as <nil>.
		requested := reflect.TypeFor[T]().String()

		return zero, errs.New(
			errs.M("As: value holds %T, not %s", payload, requested),
			errs.C(errorClass, errs.TypeCastingError),
			errs.D("held", fmt.Sprintf("%T", payload)),
			errs.D("requested", requested))
	}

	return t, nil
}
