package operations

import (
	"github.com/dr-dobermann/gobpm/pkg/expression/gep"
	vars "github.com/dr-dobermann/gobpm/pkg/variables"
)

var mulFunction = "mul"

func mulInt(y *vars.Variable, resName string) (gep.OpFunc, error) {
	return func(x *vars.Variable) (*vars.Variable, error) {
			return vars.V(resName, vars.Int, x.I*y.Int()), nil
		},
		nil
}

func mulFloat(y *vars.Variable, resName string) (gep.OpFunc, error) {
	return func(x *vars.Variable) (*vars.Variable, error) {
			return vars.V(resName, vars.Float, x.F*y.Float64()), nil
		},
		nil
}

func Mul(av *vars.Variable, resName string) (gep.OpFunc, error) {
	of, err := gep.GetOpFunc(mulFunction, av, resName)
	if err != nil {
		return nil, gep.NewOpErr(equalFunction, err,
			"couldn't get OpFunc")
	}

	return of, nil
}

// -----------------------------------------------------------------------------
// registration info
var (
	mulIntDef = gep.FunctionDefinition{
		OpFuncGen:         mulInt,
		EmptyParamAllowed: false,
		Checkers: []gep.FuncParamChecker{
			gep.ParamExactTypeChecker(mulFunction, vars.Int, vars.Float, vars.String),
			gep.ParamTypeChecker(vars.Int, mulFunction)},
	}

	mulFloatDef = gep.FunctionDefinition{
		OpFuncGen:         mulFloat,
		EmptyParamAllowed: false,
		Checkers: []gep.FuncParamChecker{
			gep.ParamExactTypeChecker(mulFunction, vars.Int, vars.Float, vars.String),
			gep.ParamTypeChecker(vars.Float, mulFunction)},
	}

	mulFunctions = map[vars.Type]gep.FunctionDefinition{
		vars.Int:   mulIntDef,
		vars.Float: mulFloatDef,
	}
)
