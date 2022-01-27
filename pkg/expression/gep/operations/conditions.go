package operations

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/expression/gep"
	vars "github.com/dr-dobermann/gobpm/pkg/variables"
)

type conditionType uint8

const (
	CondEqual conditionType = iota
	CondNotEqual
	CondLess
	CondGreater
	CondGE
	CondLE
)

func (ct conditionType) String() string {
	return []string{"CondEqual", "CondNotEqual",
		"CondLess", "CondGreater",
		"CondGE", "CondLE"}[ct]
}

type condCheckItem struct {
	condChecker func(ct conditionType, x, y *vars.Variable) bool
	types       []conditionType
}

var condTypeCompatibility = map[vars.Type]condCheckItem{
	vars.Int: {
		condChecker: IntCondChecker,
		types:       []conditionType{CondEqual, CondNotEqual, CondLess, CondGreater, CondGE, CondLE}},

	vars.Bool: {
		condChecker: IntCondChecker,
		types:       []conditionType{CondEqual, CondNotEqual}},

	vars.String: {
		condChecker: IntCondChecker,
		types:       []conditionType{CondEqual, CondNotEqual, CondLess, CondGreater, CondGE, CondLE}},

	vars.Float: {
		condChecker: IntCondChecker,
		types:       []conditionType{CondEqual, CondNotEqual, CondLess, CondGreater, CondGE, CondLE}},

	vars.Time: {
		condChecker: IntCondChecker,
		types:       []conditionType{CondEqual, CondNotEqual, CondLess, CondGreater, CondGE, CondLE}},
}

func IntCondChecker(ct conditionType, x, y *vars.Variable) bool {
	switch ct {
	case CondEqual:
		return x.I == y.Int()

	case CondNotEqual:
		return x.I != y.Int()

	case CondLess:
		return x.I < y.Int()

	case CondGreater:
		return x.I > y.Int()

	case CondGE:
		return x.I >= y.Int()

	case CondLE:
		return x.I <= y.Int()

	}

	return false
}

func BoolCondChecker(ct conditionType, x, y *vars.Variable) bool {
	switch ct {
	case CondEqual:
		return x.B == y.Bool()

	case CondNotEqual:
		return x.B != y.Bool()

	default:
		panic(fmt.Sprintf("condition '%v' isn't parovided by bool", ct))

	}

	return false
}

func StringCondChecker(ct conditionType, x, y *vars.Variable) bool {
	switch ct {
	case CondEqual:
		return x.S == y.StrVal()

	case CondNotEqual:
		return x.S != y.StrVal()

	case CondLess:
		return x.S < y.StrVal()

	case CondGreater:
		return x.S > y.StrVal()

	case CondGE:
		return x.S >= y.StrVal()

	case CondLE:
		return x.S <= y.StrVal()

	}

	return false
}

func FloatCondChecker(ct conditionType, x, y *vars.Variable) bool {
	switch ct {
	case CondEqual:
		return x.F == y.Float64()

	case CondNotEqual:
		return x.F != y.Float64()

	case CondLess:
		return x.F < y.Float64()

	case CondGreater:
		return x.F > y.Float64()

	case CondGE:
		return x.F >= y.Float64()

	case CondLE:
		return x.F <= y.Float64()

	}

	return false
}

func TimeCondChecker(ct conditionType, x, y *vars.Variable) bool {

	switch ct {
	case CondEqual:
		return x.T.Equal(y.Time())

	case CondNotEqual:
		return !x.T.Equal(y.Time())

	case CondLess:
		return x.T.UnixMilli() < y.Time().UnixMilli()

	case CondGreater:
		return x.T.UnixMilli() > y.Time().UnixMilli()

	case CondGE:
		return x.T.UnixMilli() >= y.Time().UnixMilli()

	case CondLE:
		return x.T.UnixMilli() <= y.Time().UnixMilli()

	}

	return false
}

func checkCond(ct conditionType, x, y *vars.Variable) bool {
	switch x.Type() {
	case vars.Int:
		return IntCondChecker(ct, x, y)

	case vars.Bool:
		return BoolCondChecker(ct, x, y)

	case vars.String:
		return StringCondChecker(ct, x, y)

	case vars.Float:
		return FloatCondChecker(ct, x, y)

	case vars.Time:
		return TimeCondChecker(ct, x, y)
	}
	return false
}

func checkCondCmptblty(t vars.Type, ct conditionType) bool {
	cci := condTypeCompatibility[t]
	for _, cc := range cci.types {
		if cc == ct {
			return true
		}
	}

	return false
}

func getCondFunc(
	ct conditionType,
	y *vars.Variable,
	resName string) (gep.OpFunc, error) {

	return func(x *vars.Variable) (*vars.Variable, error) {
			if !checkCondCmptblty(x.Type(), ct) {
				return nil, gep.NewOpErr(ct.String(), nil,
					"conditional operation is not supported for %s(%v)",
				)
			}

			return vars.V(resName, vars.Bool, checkCond(ct, x, y)), nil
		},
		nil
}

// type OpFuncGenerator func(y *vars.Variable, resName string) (OpFunc, error)

// -----------------------------------------------------------------------------
//        Equal
// -----------------------------------------------------------------------------
var equalFunction = "equal"

func equalAny(y *vars.Variable, resName string) (gep.OpFunc, error) {
	return getCondFunc(CondEqual, y, resName)
}

// create an OpFunc which checks if x is equal to y parameter.
// new function returns a Bool Variable with result of comparison.
//
// if error occured, error returned with nil result Variable.
//
// if resName is not empty returned Variable takes this name. If it's empty,
// then returned Variable takes OpFunc param v's name.
func Equal(av *vars.Variable, resName string) (gep.OpFunc, error) {

	of, err := gep.GetOpFunc(equalFunction, av, resName)
	if err != nil {
		return nil, err
	}

	return of, nil
}

// -----------------------------------------------------------------------------
// equal registration info
var (
	equalIntDef = gep.FunctionDefinition{
		OpFuncGen:         equalAny,
		EmptyParamAllowed: false,
		Checkers: []gep.FuncParamChecker{
			gep.ParamTypeChecker(vars.Int, equalFunction),
		}}

	equalBoolDef = gep.FunctionDefinition{
		OpFuncGen:         equalAny,
		EmptyParamAllowed: false,
		Checkers: []gep.FuncParamChecker{
			gep.ParamTypeChecker(vars.Bool, equalFunction)}}

	equalStrDef = gep.FunctionDefinition{
		OpFuncGen:         equalAny,
		EmptyParamAllowed: false,
		Checkers: []gep.FuncParamChecker{
			gep.ParamTypeChecker(vars.String, equalFunction)},
	}

	equalFloatDef = gep.FunctionDefinition{
		OpFuncGen:         equalAny,
		EmptyParamAllowed: false,
		Checkers: []gep.FuncParamChecker{
			gep.ParamTypeChecker(vars.Float, equalFunction)},
	}

	equalTimeDef = gep.FunctionDefinition{
		OpFuncGen:         equalAny,
		EmptyParamAllowed: false,
		Checkers: []gep.FuncParamChecker{
			gep.ParamTypeChecker(vars.Int, equalFunction)},
	}

	equalFunctions = map[vars.Type]gep.FunctionDefinition{
		vars.Int:    equalIntDef,
		vars.Bool:   equalBoolDef,
		vars.String: equalStrDef,
		vars.Float:  equalFloatDef,
		vars.Time:   equalTimeDef,
	}
)
