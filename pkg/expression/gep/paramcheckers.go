package gep

import (
	vars "github.com/dr-dobermann/gobpm/pkg/variables"
)

// checks if v could be converted to type t
func ParamTypeChecker(t vars.Type, funcName string) FuncParamChecker {
	return func(v *vars.Variable) error {
		if !v.CanConvertTo(t) {
			return NewOpErr(funcName, nil, "couldn't convert %q to %v",
				v.Name(), t)
		}

		return nil
	}
}

// checks if variable has any of listed types t
func ParamExactTypeChecker(funcName string, t ...vars.Type) FuncParamChecker {
	return func(v *vars.Variable) error {
		for _, ct := range t {
			if ct == v.Type() {
				return nil
			}
		}

		return NewOpErr(funcName, nil, "%q should be %v, is %v",
			v.Name(), t, v.Type())
	}
}
