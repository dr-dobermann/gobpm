package expression

import (
	"github.com/dr-dobermann/gobpm/internal/errs"
	"github.com/dr-dobermann/gobpm/model/base"
	"github.com/dr-dobermann/gobpm/pkg/identity"
	vars "github.com/dr-dobermann/gobpm/pkg/variables"
)

const (
	InvVariable = "InvalidVariable"
)

type Expression interface {
	Evaluate() error
	GetResult() (vars.Variable, error)
	Copy() Expression
	ReturnType() vars.Type
}

type ExpressionState uint8

const (
	// Expression created but all variables have empty values
	Created ExpressionState = iota

	// Parameters set by SetParams call
	Parameterized

	// Expression was successfuly evaluated
	Evaluated

	// Evaluation failed
	Error
)

type FormalExpression struct {
	base.BaseElement

	language string // Formal Expression language (FEEL) in URI format
	body     []byte

	state      ExpressionState
	parameters []*vars.Variable
	retType    vars.Type
}

func (e *FormalExpression) Language() string {
	return e.language
}

func (e *FormalExpression) ReturnType() vars.Type {
	return e.retType
}

func (e *FormalExpression) Evaluate() error {
	return errs.ErrDummyFuncImplementation
}

func (e *FormalExpression) GetResult() (vars.Variable, error) {
	return *vars.V(InvVariable, vars.Bool, false), errs.ErrDummyFuncImplementation
}

func (e *FormalExpression) Copy() Expression {
	ec := FormalExpression{
		BaseElement: e.BaseElement,
		language:    e.language,
		body:        e.body,
		retType:     e.retType}

	ec.SetNewID(identity.NewID())

	return &ec
}
