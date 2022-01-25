package operations

import (
	"strings"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/expression/gep"
	vars "github.com/dr-dobermann/gobpm/pkg/variables"
)

const (
	invalidResVar = "INVALID_OPERATION_RESULT"
)

// create an OpFunc which adds av to the opFunc parameter.
// new function returns a Variable with result of sum of av and
// OpFunc parameter v if there is no err and addition is allowed
// for the variables.
//
// if error occured, error returned with Variable named INVALID_OPERATION_RESULT
// of bool type with false value.
//
// if resName is not empty returned Variable takes this name. If it's empty,
// then returned Variable takes OpFunc param v's name.
func Add(av *vars.Variable, resName string) gep.OpFunc {
	opName := "Add"

	if len(strings.Trim(resName, " ")) == 0 {
		resName = av.Name()
	}

	add := func(v *vars.Variable) (*vars.Variable, error) {
		var res *vars.Variable

		switch v.Type() {
		case vars.Int:
			if !av.CanConvertTo(vars.Int) {
				return nil,
					gep.NewOpErr(opName, nil,
						"cannot convert %q to int", av.Name())
			}

			res = vars.V(resName, vars.Int, v.I+av.Int())

		case vars.Bool:
			return nil,
				gep.NewOpErr(opName, nil,
					"cannot add anything to bool variable %q", av.Name())

		case vars.String:
			res = vars.V(resName, vars.String, v.S+av.StrVal())

		case vars.Float:
			if !av.CanConvertTo(vars.Float) {
				return nil,
					gep.NewOpErr(opName, nil,
						"cannot convert %q to float64", av.Name())
			}

			res = vars.V(resName, vars.Float, v.F+av.Float64())

		case vars.Time:
			if av.Type() != vars.Int ||
				!av.CanConvertTo(vars.Int) {
				return nil,
					gep.NewOpErr(opName, nil,
						"couldn't add to time.Time() anything but "+
							"time.Duration(Int) values to %q", v.Name())
			}

			res = vars.V(resName, vars.Time, v.T.Add(time.Duration(av.Int())))
		}

		return res, nil
	}

	return gep.OpFunc(add)
}
