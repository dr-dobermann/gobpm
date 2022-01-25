package operations

import (
	"fmt"
	"os"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/expression/gep"
	vars "github.com/dr-dobermann/gobpm/pkg/variables"
)

type OpFuncGenerator func(y *vars.Variable, resName string) (gep.OpFunc, error)

type OpParamChecker func(v *vars.Variable) error

type OperationDescriptor struct {
	OpFuncGen OpFuncGenerator
	Checkers  []OpParamChecker
}

var mulOperation = "mul"

func mulInt(y *vars.Variable, resName string) (gep.OpFunc, error) {
	mul := func(x *vars.Variable) (*vars.Variable, error) {
		return vars.V(resName, vars.Int, x.I*y.Int()), nil
	}

	return gep.OpFunc(mul), nil
}

var mulIntDesr = OperationDescriptor{
	OpFuncGen: mulInt,
	Checkers: []OpParamChecker{
		ParamTypeChecker(vars.Int, mulOperation)},
}

func mulFloat(y *vars.Variable, resName string) (gep.OpFunc, error) {
	mul := func(x *vars.Variable) (*vars.Variable, error) {
		return vars.V(resName, vars.Float, x.F*y.Float64()), nil
	}

	return gep.OpFunc(mul), nil
}

var mulFloatDescr = OperationDescriptor{
	OpFuncGen: mulFloat,
	Checkers: []OpParamChecker{
		ParamTypeChecker(vars.Float, mulOperation)},
}

func ParamTypeChecker(t vars.Type, mulOperation string) OpParamChecker {
	pChecker := func(v *vars.Variable) error {
		if !v.CanConvertTo(vars.Int) {
			return gep.NewOpErr(mulOperation, nil, "couldn't convert %q to %v",
				v.Name(), t)
		}

		return nil
	}

	return OpParamChecker(pChecker)
}

var funcMatrix = map[string]map[vars.Type]OperationDescriptor{
	mulOperation: {
		vars.Int:   mulIntDesr,
		vars.Float: mulFloatDescr,
	},
}

func Mul(av *vars.Variable, resName string) gep.OpFunc {
	if len(strings.Trim(resName, " ")) == 0 {
		resName = av.Name()
	}

	of, err := getOpFunc(mulOperation, av, resName)
	if err != nil {
		fmt.Fprintf(os.Stderr, err.Error())

		return nil
	}

	return of
}

func getOpFunc(
	funcName string,
	y *vars.Variable,
	resName string) (gep.OpFunc, error) {

	fd, ok := funcMatrix[funcName]
	if !ok {
		return nil,
			gep.NewOpErr(funcName, nil,
				"function isn't existed in function matrix")
	}

	opFunc := func(x *vars.Variable) (*vars.Variable, error) {
		od, ok := fd[x.Type()]
		if !ok {
			return nil,
				gep.NewOpErr(
					funcName, nil,
					"operation isn't supported for %v type (%s)",
					x.Type(), x.Name())
		}

		for _, chk := range od.Checkers {
			if err := chk(y); err != nil {
				return nil,
					gep.NewOpErr(funcName, err,
						"parameter %q check failed", y.Name())
			}
		}

		f, err := od.OpFuncGen(y, resName)
		if err != nil {
			return nil, gep.NewOpErr(funcName, err,
				"couldn't get functor")
		}

		return f(x)
	}

	return opFunc, nil
}
