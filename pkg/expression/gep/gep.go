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
)

// OpFunc is a single step of the expression operation conveyer.
type OpFunc func(v *vars.Variable) (vars.Variable, error)

// GEP keeps state of a single GEP instance
type GEP struct {
	expr.FormalExpression

	// operations conveyer
	operations []OpFunc

	// result keeps current GEP and final result of expression
	// conveyer
	result vars.Variable
}

func New(id mid.Id, rt vars.Type) *GEP {
	gep := GEP{
		FormalExpression: *expr.New(id, gepLanguage, rt),
		operations:       []OpFunc{},
		result:           vars.Variable{}}

	return &gep
}
