package gep

import (
	expr "github.com/dr-dobermann/gobpm/pkg/expression"
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
// For a binary operation, the functor generator should be used.
//
// For example, if you need to create an operation like res = x + y,
// you should call gep.Add(y, res) to generate add(x), which adds y to x and
// returns Variable named res on success.
type OpFunc func(x *vars.Variable) (*vars.Variable, error)

// ParameterLoader is a function which returns an Variable which is used
// as OpFunc left parameter.
//
// The simplest ParameterLoader implementation is the gep.LoadVar, which
// takes a vars.Variable and return it as an Operation parameter.
type ParameterLoader func() (*vars.Variable, error)

// Operation describes a single step execution of GEP expression.
//
// To implement x = x + y expression next Operation shoulb be created:
//
//	     AddOp := Operation{
//		                Func: Add(variables.V("y", variables.Int, yVal), "x")
//	                 ParamLdr: func() (*variables.Variable, error) {
//								      // load x from somewhere
//	                               // and create variable with
//	                               // it's value -- xVar
//
//	                               return &xVar, nil
//	                           }
//	              }
//
// Result of sucessefully executed operation will be stored
// as intermediate or final GEP's result in GEP structure.
type Operation struct {
	Func OpFunc

	// if ParamLdr is nil, then the current result of GEP is used.
	ParamLdr ParameterLoader
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

// OpFuncGenerator returns a generated OpFunc which could
// implement function looks like res = x op y.
//
// If there is necessity to have more complicated or simpler operation
// the user just creates its-own generator which returns special OpFunc
type OpFuncGenerator func(y *vars.Variable, resName string) (OpFunc, error)

// FuncParamChecker is a function which checks right parameter of
// res = x op y expression.
// The simpliest realization of this checker is parameter type checker
// like ParamTypeChecker
type FuncParamChecker func(y *vars.Variable) error

// Single OpFunc generator definition
type FunctionDefinition struct {
	OpFuncGen OpFuncGenerator

	// if function doesn't demand parameter,
	// EmptyParamAllowed is set to true and all Checkers are ignored
	EmptyParamAllowed bool

	// the queue of parameter checkers which executed one by one
	Checkers []FuncParamChecker
}

// funcMatrix is a map of registered OpFunc functions.
//
// funcMatrix = map[functionName]map[leftParamType]FunctionDefininition
// Defined function works only with declared Variable types.
//
// nolint: gochecknoglobals
var funcMatrix = map[string]map[vars.Type]FunctionDefinition{}
