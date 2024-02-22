package values

import (
	"reflect"

	"github.com/dr-dobermann/gobpm/pkg/errs"
)

type Int int

func NewInt(i int) Int {
	return Int(i)
}

func (i *Int) Get() any {
	return int(*i)
}

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
