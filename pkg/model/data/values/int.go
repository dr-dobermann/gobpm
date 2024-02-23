package values

import (
	"reflect"

	"github.com/dr-dobermann/gobpm/pkg/errs"
)

// Int ia a cover type for int. It provides data.Value interface
// implementation for the int to use ints in any related to
// ItemDefinition classes.
type Int int

// NewInt creates a new Int object with given value.
func NewInt(i int) Int {
	return Int(i)
}

// Get returns value of the Int.
func (i *Int) Get() any {
	return int(*i)
}

// Update sets new value of the Int.
func (i *Int) Update(value any) error {
	iv, ok := value.(int)
	if !ok {
		return &errs.ApplicationError{
			Message: "couldn't convert value into to int",
			Classes: []string{
				errorClass,
				errs.InvalidParameter,
			},
			Details: map[string]string{
				"invalid_type": reflect.TypeOf(value).String(),
			},
		}
	}

	*i = Int(iv)

	return nil
}
