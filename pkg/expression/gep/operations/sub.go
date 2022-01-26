package operations

import (
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/expression/gep"
	vars "github.com/dr-dobermann/gobpm/pkg/variables"
)

var subFunction = "sub"

func subInt(y *vars.Variable, resName string) (gep.OpFunc, error) {

	return func(x *vars.Variable) (*vars.Variable, error) {
			return vars.V(resName, vars.Int, x.I-y.Int()), nil
		},
		nil
}

func subFloat(y *vars.Variable, resName string) (gep.OpFunc, error) {

	return func(x *vars.Variable) (*vars.Variable, error) {

			return vars.V(resName, vars.Float, x.F-y.Float64()), nil
		},
		nil
}

// implements substraction operation which substracts av from
// opFunc parameter v.
//
// if there is no error and sunstraction is illegible for v and av,
// opFunc return result named resName and nil error.
// if resName is empty, then v name set to result variable.
func Sub(av *vars.Variable, resName string) (gep.OpFunc, error) {
	if len(strings.Trim(resName, " ")) == 0 {
		resName = av.Name()
	}

	of, err := gep.GetOpFunc(subFunction, av, resName)
	if err != nil {
		return nil, err
	}

	return of, nil

}

// -----------------------------------------------------------------------------
// registration info
var (
	subIntDef = gep.FunctionDefinition{
		OpFuncGen:         subInt,
		EmptyParamAllowed: false,
		Checkers: []gep.FuncParamChecker{
			gep.ParamTypeChecker(vars.Int, subFunction)},
	}

	subFloatDef = gep.FunctionDefinition{
		OpFuncGen:         subFloat,
		EmptyParamAllowed: false,
		Checkers: []gep.FuncParamChecker{
			gep.ParamTypeChecker(vars.Float, subFunction)},
	}

	subFunctions = map[vars.Type]gep.FunctionDefinition{
		vars.Int:   subIntDef,
		vars.Float: subFloatDef,
	}
)
