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
	types []conditionType
}

var condTypeCompatibility = map[vars.Type]condCheckItem{
	vars.Int: {
		types: []conditionType{CondEqual, CondNotEqual,
			CondLess, CondGreater, CondGE, CondLE}},

	vars.Bool: {
		types: []conditionType{CondEqual, CondNotEqual}},

	vars.String: {
		types: []conditionType{CondEqual, CondNotEqual,
			CondLess, CondGreater, CondGE, CondLE}},

	vars.Float: {
		types: []conditionType{CondEqual, CondNotEqual,
			CondLess, CondGreater, CondGE, CondLE}},

	vars.Time: {
		types: []conditionType{CondEqual, CondNotEqual,
			CondLess, CondGreater, CondGE, CondLE}},
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

//nolint: exhaustive
func BoolCondChecker(ct conditionType, x, y *vars.Variable) bool {
	switch ct {
	case CondEqual:
		return x.B == y.Bool()

	case CondNotEqual:
		return x.B != y.Bool()

	default:
		panic(fmt.Sprintf("condition '%v' isn't parovided by bool", ct))
	}
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
				return nil,
					gep.NewOpErr(ct.String(), nil,
						"conditional operation is not supported for %s(%v)",
						x.Name(), x.Type())
			}

			return vars.V(resName, vars.Bool, checkCond(ct, x, y)), nil
		},
		nil
}

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
// if error occurred, error returned with nil result Variable.
//
// if resName is not empty returned Variable takes this name. If it's empty,
// then returned Variable takes OpFunc param v's name.
func Equal(av *vars.Variable, resName string) (gep.OpFunc, error) {
	of, err := gep.GetOpFunc(equalFunction, av, resName)
	if err != nil {
		return nil, gep.NewOpErr(equalFunction, err,
			"couldn't get OpFunc")
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
			gep.ParamTypeChecker(vars.Int, equalFunction)}}

	equalBoolDef = gep.FunctionDefinition{
		OpFuncGen:         equalAny,
		EmptyParamAllowed: false,
		Checkers: []gep.FuncParamChecker{
			gep.ParamTypeChecker(vars.Bool, equalFunction)}}

	equalStrDef = gep.FunctionDefinition{
		OpFuncGen:         equalAny,
		EmptyParamAllowed: false,
		Checkers: []gep.FuncParamChecker{
			gep.ParamTypeChecker(vars.String, equalFunction)}}

	equalFloatDef = gep.FunctionDefinition{
		OpFuncGen:         equalAny,
		EmptyParamAllowed: false,
		Checkers: []gep.FuncParamChecker{
			gep.ParamTypeChecker(vars.Float, equalFunction)}}

	equalTimeDef = gep.FunctionDefinition{
		OpFuncGen:         equalAny,
		EmptyParamAllowed: false,
		Checkers: []gep.FuncParamChecker{
			gep.ParamTypeChecker(vars.Time, equalFunction)}}

	equalFunctions = map[vars.Type]gep.FunctionDefinition{
		vars.Int:    equalIntDef,
		vars.Bool:   equalBoolDef,
		vars.String: equalStrDef,
		vars.Float:  equalFloatDef,
		vars.Time:   equalTimeDef,
	}
)

// -----------------------------------------------------------------------------
//        NotEqual
// -----------------------------------------------------------------------------
var notEqualFunction = "notEqual"

func notEqualAny(y *vars.Variable, resName string) (gep.OpFunc, error) {
	return getCondFunc(CondNotEqual, y, resName)
}

// create an OpFunc which checks if x is equal to y parameter.
// new function returns a Bool Variable with result of comparison.
//
// if error occurred, error returned with nil result Variable.
//
// if resName is not empty returned Variable takes this name. If it's empty,
// then returned Variable takes OpFunc param v's name.
func NotEqual(av *vars.Variable, resName string) (gep.OpFunc, error) {
	of, err := gep.GetOpFunc(notEqualFunction, av, resName)
	if err != nil {
		return nil, gep.NewOpErr(equalFunction, err,
			"couldn't get OpFunc")
	}

	return of, nil
}

// -----------------------------------------------------------------------------
// notEqual registration info
var (
	notEqualIntDef = gep.FunctionDefinition{
		OpFuncGen:         notEqualAny,
		EmptyParamAllowed: false,
		Checkers: []gep.FuncParamChecker{
			gep.ParamTypeChecker(vars.Int, notEqualFunction)}}

	notEqualBoolDef = gep.FunctionDefinition{
		OpFuncGen:         notEqualAny,
		EmptyParamAllowed: false,
		Checkers: []gep.FuncParamChecker{
			gep.ParamTypeChecker(vars.Bool, notEqualFunction)}}

	notEqualStrDef = gep.FunctionDefinition{
		OpFuncGen:         notEqualAny,
		EmptyParamAllowed: false,
		Checkers: []gep.FuncParamChecker{
			gep.ParamTypeChecker(vars.String, notEqualFunction)}}

	notEqualFloatDef = gep.FunctionDefinition{
		OpFuncGen:         notEqualAny,
		EmptyParamAllowed: false,
		Checkers: []gep.FuncParamChecker{
			gep.ParamTypeChecker(vars.Float, notEqualFunction)}}

	notEqualTimeDef = gep.FunctionDefinition{
		OpFuncGen:         notEqualAny,
		EmptyParamAllowed: false,
		Checkers: []gep.FuncParamChecker{
			gep.ParamTypeChecker(vars.Time, notEqualFunction)}}

	notEqualFunctions = map[vars.Type]gep.FunctionDefinition{
		vars.Int:    notEqualIntDef,
		vars.Bool:   notEqualBoolDef,
		vars.String: notEqualStrDef,
		vars.Float:  notEqualFloatDef,
		vars.Time:   notEqualTimeDef,
	}
)

// -----------------------------------------------------------------------------
//        Less
// -----------------------------------------------------------------------------
var lessFunction = "less"

func lessAny(y *vars.Variable, resName string) (gep.OpFunc, error) {
	return getCondFunc(CondLess, y, resName)
}

// create an OpFunc which checks if x is equal to y parameter.
// new function returns a Bool Variable with result of comparison.
//
// if error occurred, error returned with nil result Variable.
//
// if resName is not empty returned Variable takes this name. If it's empty,
// then returned Variable takes OpFunc param v's name.
func GreaterEqual(av *vars.Variable, resName string) (gep.OpFunc, error) {
	of, err := gep.GetOpFunc(lessFunction, av, resName)
	if err != nil {
		return nil, gep.NewOpErr(equalFunction, err,
			"couldn't get OpFunc")
	}

	return of, nil
}

// -----------------------------------------------------------------------------
// less registration info
var (
	lessIntDef = gep.FunctionDefinition{
		OpFuncGen:         lessAny,
		EmptyParamAllowed: false,
		Checkers: []gep.FuncParamChecker{
			gep.ParamTypeChecker(vars.Int, lessFunction)}}

	lessBoolDef = gep.FunctionDefinition{
		OpFuncGen:         lessAny,
		EmptyParamAllowed: false,
		Checkers: []gep.FuncParamChecker{
			gep.ParamTypeChecker(vars.Bool, lessFunction)}}

	lessStrDef = gep.FunctionDefinition{
		OpFuncGen:         lessAny,
		EmptyParamAllowed: false,
		Checkers: []gep.FuncParamChecker{
			gep.ParamTypeChecker(vars.String, lessFunction)}}

	lessFloatDef = gep.FunctionDefinition{
		OpFuncGen:         lessAny,
		EmptyParamAllowed: false,
		Checkers: []gep.FuncParamChecker{
			gep.ParamTypeChecker(vars.Float, lessFunction)}}

	lessTimeDef = gep.FunctionDefinition{
		OpFuncGen:         lessAny,
		EmptyParamAllowed: false,
		Checkers: []gep.FuncParamChecker{
			gep.ParamTypeChecker(vars.Time, lessFunction)}}

	lessFunctions = map[vars.Type]gep.FunctionDefinition{
		vars.Int:    lessIntDef,
		vars.Bool:   lessBoolDef,
		vars.String: lessStrDef,
		vars.Float:  lessFloatDef,
		vars.Time:   lessTimeDef,
	}
)

// -----------------------------------------------------------------------------
//        Greater
// -----------------------------------------------------------------------------
var grtrFunction = "greater"

func grtrAny(y *vars.Variable, resName string) (gep.OpFunc, error) {
	return getCondFunc(CondGreater, y, resName)
}

// create an OpFunc which checks if x is equal to y parameter.
// new function returns a Bool Variable with result of comparison.
//
// if error occurred, error returned with nil result Variable.
//
// if resName is not empty returned Variable takes this name. If it's empty,
// then returned Variable takes OpFunc param v's name.
func LessEqual(av *vars.Variable, resName string) (gep.OpFunc, error) {
	of, err := gep.GetOpFunc(grtrFunction, av, resName)
	if err != nil {
		return nil, gep.NewOpErr(equalFunction, err,
			"couldn't get OpFunc")
	}

	return of, nil
}

// -----------------------------------------------------------------------------
// Greater registration info
var (
	grtrIntDef = gep.FunctionDefinition{
		OpFuncGen:         grtrAny,
		EmptyParamAllowed: false,
		Checkers: []gep.FuncParamChecker{
			gep.ParamTypeChecker(vars.Int, grtrFunction)}}

	grtrBoolDef = gep.FunctionDefinition{
		OpFuncGen:         grtrAny,
		EmptyParamAllowed: false,
		Checkers: []gep.FuncParamChecker{
			gep.ParamTypeChecker(vars.Bool, grtrFunction)}}

	grtrStrDef = gep.FunctionDefinition{
		OpFuncGen:         grtrAny,
		EmptyParamAllowed: false,
		Checkers: []gep.FuncParamChecker{
			gep.ParamTypeChecker(vars.String, grtrFunction)}}

	grtrFloatDef = gep.FunctionDefinition{
		OpFuncGen:         grtrAny,
		EmptyParamAllowed: false,
		Checkers: []gep.FuncParamChecker{
			gep.ParamTypeChecker(vars.Float, grtrFunction)}}

	grtrTimeDef = gep.FunctionDefinition{
		OpFuncGen:         grtrAny,
		EmptyParamAllowed: false,
		Checkers: []gep.FuncParamChecker{
			gep.ParamTypeChecker(vars.Time, grtrFunction)}}

	grtrFunctions = map[vars.Type]gep.FunctionDefinition{
		vars.Int:    grtrIntDef,
		vars.Bool:   grtrBoolDef,
		vars.String: grtrStrDef,
		vars.Float:  grtrFloatDef,
		vars.Time:   grtrTimeDef,
	}
)

// -----------------------------------------------------------------------------
//        GE
// -----------------------------------------------------------------------------
var geFunction = "greaterOrEqual"

func geAny(y *vars.Variable, resName string) (gep.OpFunc, error) {
	return getCondFunc(CondGE, y, resName)
}

// create an OpFunc which checks if x is equal to y parameter.
// new function returns a Bool Variable with result of comparison.
//
// if error occurred, error returned with nil result Variable.
//
// if resName is not empty returned Variable takes this name. If it's empty,
// then returned Variable takes OpFunc param v's name.
func GE(av *vars.Variable, resName string) (gep.OpFunc, error) {
	of, err := gep.GetOpFunc(geFunction, av, resName)
	if err != nil {
		return nil, gep.NewOpErr(equalFunction, err,
			"couldn't get OpFunc")
	}

	return of, nil
}

// -----------------------------------------------------------------------------
// GE registration info
var (
	geIntDef = gep.FunctionDefinition{
		OpFuncGen:         geAny,
		EmptyParamAllowed: false,
		Checkers: []gep.FuncParamChecker{
			gep.ParamTypeChecker(vars.Int, geFunction)}}

	geBoolDef = gep.FunctionDefinition{
		OpFuncGen:         geAny,
		EmptyParamAllowed: false,
		Checkers: []gep.FuncParamChecker{
			gep.ParamTypeChecker(vars.Bool, geFunction)}}

	geStrDef = gep.FunctionDefinition{
		OpFuncGen:         geAny,
		EmptyParamAllowed: false,
		Checkers: []gep.FuncParamChecker{
			gep.ParamTypeChecker(vars.String, geFunction)}}

	geFloatDef = gep.FunctionDefinition{
		OpFuncGen:         geAny,
		EmptyParamAllowed: false,
		Checkers: []gep.FuncParamChecker{
			gep.ParamTypeChecker(vars.Float, geFunction)}}

	geTimeDef = gep.FunctionDefinition{
		OpFuncGen:         geAny,
		EmptyParamAllowed: false,
		Checkers: []gep.FuncParamChecker{
			gep.ParamTypeChecker(vars.Time, geFunction)}}

	geFunctions = map[vars.Type]gep.FunctionDefinition{
		vars.Int:    geIntDef,
		vars.Bool:   geBoolDef,
		vars.String: geStrDef,
		vars.Float:  geFloatDef,
		vars.Time:   geTimeDef,
	}
)

// -----------------------------------------------------------------------------
//        LE
// -----------------------------------------------------------------------------
var leFunction = "lessOrEqual"

func leAny(y *vars.Variable, resName string) (gep.OpFunc, error) {
	return getCondFunc(CondLE, y, resName)
}

// create an OpFunc which checks if x is equal to y parameter.
// new function returns a Bool Variable with result of comparison.
//
// if error occurred, error returned with nil result Variable.
//
// if resName is not empty returned Variable takes this name. If it's empty,
// then returned Variable takes OpFunc param v's name.
func LE(av *vars.Variable, resName string) (gep.OpFunc, error) {
	of, err := gep.GetOpFunc(leFunction, av, resName)
	if err != nil {
		return nil, gep.NewOpErr(equalFunction, err,
			"couldn't get OpFunc")
	}

	return of, nil
}

// -----------------------------------------------------------------------------
// LE registration info
var (
	leIntDef = gep.FunctionDefinition{
		OpFuncGen:         leAny,
		EmptyParamAllowed: false,
		Checkers: []gep.FuncParamChecker{
			gep.ParamTypeChecker(vars.Int, leFunction)}}

	leBoolDef = gep.FunctionDefinition{
		OpFuncGen:         leAny,
		EmptyParamAllowed: false,
		Checkers: []gep.FuncParamChecker{
			gep.ParamTypeChecker(vars.Bool, leFunction)}}

	leStrDef = gep.FunctionDefinition{
		OpFuncGen:         leAny,
		EmptyParamAllowed: false,
		Checkers: []gep.FuncParamChecker{
			gep.ParamTypeChecker(vars.String, leFunction)}}

	leFloatDef = gep.FunctionDefinition{
		OpFuncGen:         leAny,
		EmptyParamAllowed: false,
		Checkers: []gep.FuncParamChecker{
			gep.ParamTypeChecker(vars.Float, leFunction)}}

	leTimeDef = gep.FunctionDefinition{
		OpFuncGen:         leAny,
		EmptyParamAllowed: false,
		Checkers: []gep.FuncParamChecker{
			gep.ParamTypeChecker(vars.Time, leFunction)}}

	leFunctions = map[vars.Type]gep.FunctionDefinition{
		vars.Int:    leIntDef,
		vars.Bool:   leBoolDef,
		vars.String: leStrDef,
		vars.Float:  leFloatDef,
		vars.Time:   leTimeDef,
	}
)
