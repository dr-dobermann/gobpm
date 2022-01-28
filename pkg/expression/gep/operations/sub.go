package operations

import (
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

// implements subtraction operation which subtracts av from
// opFunc parameter v.
//
// if there is no error and sunstraction is illegible for v and av,
// opFunc return result named resName and nil error.
// if resName is empty, then v name set to result variable.
func Sub(av *vars.Variable, resName string) (gep.OpFunc, error) {
	of, err := gep.GetOpFunc(subFunction, av, resName)
	if err != nil {
		return nil, gep.NewOpErr(equalFunction, err,
			"couldn't get OpFunc")
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
			gep.ParamExactTypeChecker(subFunction, vars.Int, vars.Float, vars.String),
			gep.ParamTypeChecker(vars.Int, subFunction)},
	}

	subFloatDef = gep.FunctionDefinition{
		OpFuncGen:         subFloat,
		EmptyParamAllowed: false,
		Checkers: []gep.FuncParamChecker{
			gep.ParamExactTypeChecker(subFunction, vars.Int, vars.Float, vars.String),
			gep.ParamTypeChecker(vars.Float, subFunction)},
	}

	subFunctions = map[vars.Type]gep.FunctionDefinition{
		vars.Int:   subIntDef,
		vars.Float: subFloatDef,
	}
)
