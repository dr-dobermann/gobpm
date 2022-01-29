package extension

import (
	"github.com/dr-dobermann/gobpm/pkg/expression/gep"
	"github.com/dr-dobermann/gobpm/pkg/variables"
)

// Math function caller
func MathFuncCaller(fn func(float64) float64,
	resName string,
	paramCheckers ...gep.FuncParamChecker) (gep.OpFunc, error) {

	funcName := "MathFuncCaller"

	if fn == nil {
		return nil, gep.NewOpErr(funcName, nil,
			"have no function to call")
	}

	return func(x *variables.Variable) (*variables.Variable, error) {
		for i, chkr := range paramCheckers {
			if err := chkr(x); err != nil {
				return nil, gep.NewOpErr(funcName, err,
					"#%d check failed", i)
			}
		}

		return variables.V(resName, variables.Float, fn(x.Float64())),
			nil
	}, nil
}

func CheckPositive(v *variables.Variable) error {
	if v.Float64() < 0 {
		return gep.NewOpErr("", nil, "%q is less than 0", v.Name())
	}

	return nil
}
