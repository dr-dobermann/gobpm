package operations

import (
	"github.com/dr-dobermann/gobpm/pkg/expression/gep"
	vars "github.com/dr-dobermann/gobpm/pkg/variables"
)

var divFunction = "div"

func divInt(y *vars.Variable, resName string) (gep.OpFunc, error) {
	return func(x *vars.Variable) (*vars.Variable, error) {
			return vars.V(resName, vars.Int, x.I/y.Int()), nil
		},
		nil
}

func divFloat(y *vars.Variable, resName string) (gep.OpFunc, error) {
	return func(x *vars.Variable) (*vars.Variable, error) {
			return vars.V(resName, vars.Float, x.F/y.Float64()), nil
		},
		nil
}

func Div(av *vars.Variable, resName string) (gep.OpFunc, error) {
	of, err := gep.GetOpFunc(divFunction, av, resName)
	if err != nil {
		return nil, gep.NewOpErr(equalFunction, err,
			"couldn't get OpFunc")
	}

	return of, nil
}

func paramZeroChecker(funcName string) gep.FuncParamChecker {
	pChecker := func(v *vars.Variable) error {
		if v.Type() == vars.Int || v.Type() == vars.Float {
			if v.Int() == 0 {
				return gep.NewOpErr(funcName, nil, "%s couldn't be 0",
					v.Name())
			}
		}

		return nil
	}

	return pChecker
}

// -----------------------------------------------------------------------------
// registration info
var (
	divIntDef = gep.FunctionDefinition{
		OpFuncGen:         divInt,
		EmptyParamAllowed: false,
		Checkers: []gep.FuncParamChecker{
			gep.ParamExactTypeChecker(divFunction, vars.Int, vars.Float, vars.String),
			gep.ParamTypeChecker(vars.Int, divFunction),
			paramZeroChecker(divFunction)},
	}

	divFloatDef = gep.FunctionDefinition{
		OpFuncGen:         divFloat,
		EmptyParamAllowed: false,
		Checkers: []gep.FuncParamChecker{
			gep.ParamExactTypeChecker(divFunction, vars.Int, vars.Float, vars.String),
			gep.ParamTypeChecker(vars.Float, divFunction),
			paramZeroChecker(divFunction)},
	}

	divFunctions = map[vars.Type]gep.FunctionDefinition{
		vars.Int:   divIntDef,
		vars.Float: divFloatDef,
	}
)
