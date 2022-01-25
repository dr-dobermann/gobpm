package operations

import (
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/expression/gep"
	vars "github.com/dr-dobermann/gobpm/pkg/variables"
)

// implements substraction operation which substracts av from
// opFunc parameter v.
//
// if there is no error and sunstraction is illegible for v and av,
// opFunc return result named resName and nil error.
// if resName is empty, then v name set to result variable.
func Sub(av *vars.Variable, resName string) gep.OpFunc {
	opName := "Sub"

	if len(strings.Trim(resName, " ")) == 0 {
		resName = av.Name()
	}

	sub := func(v *vars.Variable) (vars.Variable, error) {
		var res *vars.Variable

		switch v.Type() {
		case vars.Int:
			if !av.CanConvertTo(vars.Int) {
				return *vars.V(invalidResVar, vars.Bool, false),
					gep.NewOpErr(opName, nil,
						"cannot convert %q to int", av.Name())
			}

			res = vars.V(resName, vars.Int, v.I-av.Int())

		case vars.Bool, vars.String, vars.Time:
			return *vars.V(invalidResVar, vars.Bool, false),
				gep.NewOpErr(opName, nil,
					"substraction isn't allowed for type %v", v.Type())

		case vars.Float:
			if !av.CanConvertTo(vars.Float) {
				return *vars.V(invalidResVar, vars.Bool, false),
					gep.NewOpErr(opName, nil,
						"cannot convert %q to float64", av.Name())
			}

			res = vars.V(resName, vars.Float, v.F-av.Float64())
		}

		return *res, nil
	}

	return gep.OpFunc(sub)
}
