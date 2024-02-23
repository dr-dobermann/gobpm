package values

import (
	"reflect"

	"github.com/dr-dobermann/gobpm/pkg/errs"
)

// String ia a cover type for string. It provides data.Value interface
// implementation for the string to use strings in any related to
// ItemDefinition classes.
type String string

// NewString creates a nes String object from string.
func NewString(s string) String {
	return String(s)
}

// Get returns value of the String.
func (s *String) Get() any {
	return string(*s)
}

// Update sets new value of the String.
func (s *String) Update(value any) error {
	sval, ok := value.(string)
	if !ok {
		return &errs.ApplicationError{
			Message: "couldn't convert value into to string",
			Classes: []string{
				errorClass,
				errs.InvalidParameter,
			},
			Details: map[string]string{
				"invalid_type": reflect.TypeOf(value).String(),
			},
		}
	}

	*s = (String)(sval)

	return nil
}
