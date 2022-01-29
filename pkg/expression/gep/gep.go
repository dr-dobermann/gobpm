// GEP -- GoBPM Expresiion Processor.
//
// GEP is an internal, API-oriented extensible expression processor
// for GoBPM project.
//
// Single GEP instance represents a conveyer of operations (OpFunc)
// with a single final result.
package gep

import (
	"strings"

	expr "github.com/dr-dobermann/gobpm/pkg/expression"
	mid "github.com/dr-dobermann/gobpm/pkg/identity"
	vars "github.com/dr-dobermann/gobpm/pkg/variables"
)

const (
	InvalidFuncName = "INVALID_FUNCTION_NAME"
)

func New(id mid.Id, returnType vars.Type) *GEP {
	gep := GEP{
		FormalExpression: *expr.New(id, gepLanguage, returnType),
		operations:       []Operation{},
		result:           *vars.V(resNotEvaluated, vars.Bool, false)}

	return &gep
}

func (g *GEP) SetParams(pp ...vars.Variable) error {
	return g.NewExprErr(nil, "GEP doesn't provide SetParams")
}

func (g *GEP) AddOperation(op Operation) error {
	if op.Func == nil {
		return g.NewExprErr(nil, "operation function couldn't be nil")
	}

	g.operations = append(g.operations, op)

	g.FormalExpression.UpdateState(expr.Parameterized)

	return nil
}

func (g *GEP) Evaluate() error {
	if g.State() != expr.Parameterized {
		return g.NewExprErr(nil, "operation list is empty")
	}

	for i, op := range g.operations {
		if op.Func == nil {
			return g.NewExprErr(nil,
				"OpFunc is empty for operation #%d", i)
		}

		var (
			opParam *vars.Variable
			err     error
		)

		if op.ParamLdr == nil {
			opParam = &g.result
		} else {
			opParam, err = op.ParamLdr()
			if err != nil {
				return g.NewExprErr(
					err,
					"couldn't load an left param for operation #%d",
					i)
			}
		}

		res, err := op.Func(opParam)
		if err != nil || res == nil {
			return g.NewExprErr(
				err,
				"operation #%d function execution failed",
				i)
		}

		g.result = *res
	}

	g.FormalExpression.UpdateState(expr.Evaluated)

	return nil
}

func (g *GEP) GetResult() (vars.Variable, error) {
	if g.State() != expr.Evaluated {
		return *vars.V(resNotEvaluated, vars.Bool, false),
			g.NewExprErr(nil, "GEP isn't evaluated or evaluated with errors")
	}

	if g.result.Type() != g.ReturnType() {
		return *vars.V(resNotEvaluated, vars.Bool, false),
			g.NewExprErr(
				nil,
				"current GEP result type(%v) isn't "+
					"equal to expression return type(%v)",
				g.result.Type(), g.ReturnType())
	}

	return g.result, nil
}

// -----------------------------------------------------------------------------
//    Utility functions
// -----------------------------------------------------------------------------
func LoadVar(v *vars.Variable) ParameterLoader {
	if v == nil {
		return nil
	}

	pLoader := func() (*vars.Variable, error) {
		return v, nil
	}

	return ParameterLoader(pLoader)
}

// -----------------------------------------------------------------------------
// Operation Function Generations
// -----------------------------------------------------------------------------

// creates and returns function from its FunctionDefinition.
//
//nolint: cyclop
func GetOpFunc(
	funcName string,
	y *vars.Variable,
	resName string) (OpFunc, error) {

	fd, ok := funcMatrix[funcName]
	if !ok {
		return nil,
			NewOpErr(funcName, nil,
				"function isn't existed in function matrix")
	}

	opFunc := func(x *vars.Variable) (*vars.Variable, error) {
		if strings.Trim(x.Name(), " ") == "" {
			return nil,
				NewOpErr(
					funcName, nil,
					"left variable should have non-empty name")
		}

		if strings.Trim(resName, " ") == "" {
			resName = x.Name()
		}

		od, ok := fd[x.Type()]
		if !ok {
			return nil,
				NewOpErr(
					funcName, nil,
					"operation isn't supported for %v type (%s)",
					x.Type(), x.Name())
		}

		if y == nil {
			if !od.EmptyParamAllowed {
				return nil,
					NewOpErr(funcName, nil,
						"function %q doesn't allow right parameter empty",
						funcName)
			}
		} else {
			for _, chk := range od.Checkers {
				if err := chk(y); err != nil {
					return nil,
						NewOpErr(funcName, err,
							"parameter %q check failed", y.Name())
				}
			}
		}

		f, err := od.OpFuncGen(y, resName)
		if err != nil {
			return nil, NewOpErr(funcName, err,
				"couldn't get functor")
		}

		return f(x)
	}

	return opFunc, nil
}

func AddOpFuncDefinition(
	funcName string,
	t vars.Type,
	fd FunctionDefinition) error {

	if len(strings.Trim(funcName, " ")) == 0 {
		return NewOpErr(InvalidFuncName, nil,
			"no function name")
	}

	f, ok := funcMatrix[funcName]
	if !ok {
		f = make(map[vars.Type]FunctionDefinition)
		funcMatrix[funcName] = f
	}

	if _, ok := f[t]; ok {
		return NewOpErr(funcName, nil,
			"functuin already defined for type %v", t)
	}

	f[t] = fd

	return nil
}

func GetOpFuncDefinition(
	funcName string,
	t vars.Type) (*FunctionDefinition, error) {
	if len(strings.Trim(funcName, " ")) == 0 {
		return nil, NewOpErr(InvalidFuncName, nil,
			"no function name")
	}

	f, ok := funcMatrix[funcName]
	if !ok {
		return nil, NewOpErr(funcName, nil, "function isn't found")
	}

	fd, ok := f[t]
	if !ok {
		return nil, NewOpErr(funcName, nil,
			"functuin isn't defined for type %v", t)
	}

	return &fd, nil
}
