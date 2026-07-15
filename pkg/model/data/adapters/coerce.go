package adapters

import (
	"context"
	"reflect"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

// coerce resolves an incoming value to something assignable to the target Go
// type: the raw value directly, or — when it is a data.Value (the shape the
// SetPath/SetField seams hand over) — its Get snapshot. A shape that fits
// neither way is a classified type-clash error naming op and target.
func coerce(
	ctx context.Context, op string, target reflect.Type, v any,
) (reflect.Value, error) {
	if v != nil {
		rv := reflect.ValueOf(v)
		if rv.Type().AssignableTo(target) {
			return rv, nil
		}

		if dv, ok := v.(data.Value); ok {
			raw := dv.Get(ctx)
			if raw != nil {
				rv = reflect.ValueOf(raw)
				if rv.Type().AssignableTo(target) {
					return rv, nil
				}
			}
		}
	}

	return reflect.Value{}, errs.New(
		errs.M("%s: value (%v) isn't assignable to %s",
			op, v, target.String()),
		errs.C(errorClass, errs.TypeCastingError))
}
