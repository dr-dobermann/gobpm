// GEP -- GoBPM Expresiion Processor.
//
// GEP is an internal, API-oriented extensible expression processor
// for GoBPM project.
//
// Single GEP instance represents a conveyer of operations (OpFunc)
// with a single final result.
package gep

import (
	expr "github.com/dr-dobermann/gobpm/pkg/expression"
	mid "github.com/dr-dobermann/gobpm/pkg/identity"
	vars "github.com/dr-dobermann/gobpm/pkg/variables"
)

const (
	gepLanguage = "GEP"

	resNotEvaluated = "NOT_CALCULATED_YET"
)

// OpFunc is a functor used as an Operation executor in expression
// conveyer.
//
// OpFunc uses single Variable and returns a result in a new Variable.
// If it's needed to create a binary operation, the functor creation
// function shoul be used.
//
// For example, if you need to create operation res = x + y operation,
// you should call gep.Add(y, res) to get add(x), which adds y to x and
// returns Variable named res on success.
type OpFunc func(v *vars.Variable) (vars.Variable, error)

// OperandLoader is a function which returns an Variable which is used
// as OpFunc operand.
type OperandLoader func() (*vars.Variable, error)

// Operation describes a single step execution of GEP expression.
//
// To implement x = x + y expression next Operation shoulb be created:
//
//      AddOp := Operation{
//	                Func: Add(variables.V("y", variables.Int, yVal), "x")
//                  OpLoader: func() (*variables.Variable, error) {
//							      // load x from somewhere
//                                // and create variable with
//                                // it's value -- xVar
//
//                                return &xVar, nil
//                            }
//               }
//
// Result of sucessefully executed operation will be stored
// as intermediate or final GEP's result in GEP structure.
type Operation struct {
	Func OpFunc

	// if OpLoader is nil, then the current result of GEP is used.
	OpLoader OperandLoader
}

// GEP keeps state of a single GEP instance.
//
// Result of GEP Expression calculated as sequential execution of operations.
type GEP struct {
	expr.FormalExpression

	// operations conveyer
	operations []Operation

	// result keeps current GEP and final result of expression
	// conveyer
	result vars.Variable
}

func New(id mid.Id, rt vars.Type) *GEP {
	gep := GEP{
		FormalExpression: *expr.New(id, gepLanguage, rt),
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
		var (
			opOperand *vars.Variable
			err       error
		)

		if op.OpLoader == nil {
			opOperand = &g.result
		} else {
			opOperand, err = op.OpLoader()
			if err != nil {
				return g.NewExprErr(
					err,
					"couldn't load an operand for operation #%d",
					i)
			}
		}

		res, err := op.Func(opOperand)
		if err != nil {
			return g.NewExprErr(
				err,
				"operation #%d function execution failed",
				i)
		}

		g.result = res
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
func LoadVar(v *vars.Variable) OperandLoader {
	if v == nil {
		return nil
	}

	opLoader := func() (*vars.Variable, error) {
		return v, nil
	}

	return OperandLoader(opLoader)
}
