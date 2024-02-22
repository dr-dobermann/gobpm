package values

import (
	"reflect"

	"github.com/dr-dobermann/gobpm/pkg/errs"
)

type String string

func NewString(s string) String {
	return String(s)
}

func (s *String) Get() any {
	return string(*s)
}

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
