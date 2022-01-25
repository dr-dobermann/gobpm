package operations

import (
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/expression/gep"
	vars "github.com/dr-dobermann/gobpm/pkg/variables"
)

type IntFunc func(x int64, vName string) *vars.Variable

type IntOpIntFunc func(y *vars.Variable) (IntFunc, error)

type FloatFunc func(x float64, vName string) *vars.Variable

type FloatOpFloatFunc func(y *vars.Variable) (FloatFunc, error)

type OperationDescriptor struct {
	paramY vars.Type
	opFunc interface{}
}

var mulOperation = "mul"

func mulInt(y *vars.Variable) (IntFunc, error) {
	if !y.CanConvertTo(vars.Int) {
		return nil,
			gep.NewOpErr(mulOperation, nil, "couldn't convert %q to int",
				y.Name())
	}

	mul := func(x int64, vName string) *vars.Variable {
		return vars.V(vName, vars.Int, x*y)
	}

	return IntFunc(mul), nil
}

func mulFloat(float64) (FloatFunc, error) {
	mul := func(x float64, vName string) *vars.Variable {
		return vars.V(vName, vars.Float, x*y)
	}

	return FloatFunc(mul)
}

var operationMatrix = map[string]map[vars.Type][]OperationDescriptor{
	"mul": {
		vars.Int:   {{vars.Int, IntOpIntFunc(mulInt)}},
		vars.Float: {{vars.Float, FloatFuncGenFloat}},
	},
}

func Mul(av *vars.Variable, resName string) gep.OpFunc {

	om, ok := operationMatrix[opName]
	if !ok { // operation not implemented
		return nil
	}

	if len(strings.Trim(resName, " ")) == 0 {
		resName = av.Name()
	}

	mul := func(v *vars.Variable) (vars.Variable, error) {
		var res *vars.Variable

		odl, ok := om[v.Type()]
		if !ok {
			return *vars.V(invalidResVar, vars.Bool, false),
				gep.NewOpErr(opName, nil,
					"operation %q is not supported for type %v(%s)",
					opName, v.Type(), v.Name())
		}

		for _, od := range odl {
			switch {
			case v.Type() == vars.Int && od.paramY == av.Type():
				opF, ok := od.opFunc.(IntFunc)
				if !ok {
					return *vars.V(invalidResVar, vars.Bool, false),
						gep.NewOpErr(opName, nil,
							"operation %q is not found for variables %q and %q",
							opName, v.Name(), av.Name())
				}

				mul := opF(av.I, resName)
				res = mul(v.Int())

			}

		}

		return *res, nil
	}

	return gep.OpFunc(mul)
}
