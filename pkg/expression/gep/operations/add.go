package operations

import (
	"time"

	"github.com/dr-dobermann/gobpm/pkg/expression/gep"
	vars "github.com/dr-dobermann/gobpm/pkg/variables"
)

var addFunction = "add"

func addInt(y *vars.Variable, resName string) (gep.OpFunc, error) {
	return func(x *vars.Variable) (*vars.Variable, error) {
			return vars.V(resName, vars.Int, x.I+y.Int()), nil
		},
		nil
}

func addString(y *vars.Variable, resName string) (gep.OpFunc, error) {
	return func(x *vars.Variable) (*vars.Variable, error) {
			return vars.V(resName, vars.String, x.S+y.StrVal()), nil
		},
		nil
}

func addFloat(y *vars.Variable, resName string) (gep.OpFunc, error) {
	return func(x *vars.Variable) (*vars.Variable, error) {
			return vars.V(resName, vars.Float, x.F+y.Float64()), nil
		},
		nil
}

func addTime(y *vars.Variable, resName string) (gep.OpFunc, error) {
	return func(x *vars.Variable) (*vars.Variable, error) {
			return vars.V(
					resName,
					vars.Time,
					x.T.Add(time.Duration(y.Int()))),
				nil
		},
		nil
}

// create an OpFunc which adds y to the opFunc parameter.
// new function returns a Variable with result of sum of y and
// OpFunc parameter v if there is no err and addition is allowed
// for the variables.
//
// if error occurred, error returned with nil result Variable.
//
// if resName is not empty returned Variable takes this name. If it's empty,
// then returned Variable takes OpFunc param v's name.
func Add(y *vars.Variable, resName string) (gep.OpFunc, error) {
	of, err := gep.GetOpFunc(addFunction, y, resName)
	if err != nil {
		return nil, gep.NewOpErr(addFunction, err,
			"couldn't get OpFunc")
	}

	return of, nil
}

// -----------------------------------------------------------------------------
// registration info
var (
	addIntDef = gep.FunctionDefinition{
		OpFuncGen:         addInt,
		EmptyParamAllowed: false,
		Checkers: []gep.FuncParamChecker{
			gep.ParamTypeChecker(vars.Int, addFunction)},
	}

	addStrDef = gep.FunctionDefinition{
		OpFuncGen:         addString,
		EmptyParamAllowed: false,
		Checkers: []gep.FuncParamChecker{
			gep.ParamTypeChecker(vars.String, addFunction)},
	}

	addFloatDef = gep.FunctionDefinition{
		OpFuncGen:         addFloat,
		EmptyParamAllowed: false,
		Checkers: []gep.FuncParamChecker{
			gep.ParamExactTypeChecker(addFunction, vars.Int, vars.Float, vars.String, vars.Bool),
			gep.ParamTypeChecker(vars.Float, addFunction)},
	}

	addTimeDef = gep.FunctionDefinition{
		OpFuncGen:         addTime,
		EmptyParamAllowed: false,
		Checkers: []gep.FuncParamChecker{
			gep.ParamExactTypeChecker(addFunction, vars.Int, vars.Float)},
	}

	addFunctions = map[vars.Type]gep.FunctionDefinition{
		vars.Int:    addIntDef,
		vars.String: addStrDef,
		vars.Float:  addFloatDef,
		vars.Time:   addTimeDef,
	}
)
