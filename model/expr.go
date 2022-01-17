package model

import "github.com/dr-dobermann/gobpm/internal/errs"

type Expression interface {
	Evaluate() error
	GetResult() ([]*Variable, error)
	Copy() Expression
	ExprType() ExpressionType
	ReturnType()
}

type ExpressionType uint8

const (
	ExTEmbedded ExpressionType = iota
	ExTExtended
)

type ExpressionBase struct {
	BaseElement
	language string // Formal Expression language (FEEL) in URI format
	body     []byte
	etype    ExpressionType
	retType  VarType
}

func (e *ExpressionBase) ExprType() ExpressionType {
	return e.etype
}

func (e *ExpressionBase) ReturnType() VarType {
	return e.retType
}

func (e *ExpressionBase) Evaluate() error {
	return errs.ErrDummyFuncImplementation
}

func (e *ExpressionBase) GetResult() ([]*Variable, error) {
	return nil, errs.ErrDummyFuncImplementation
}

func (e *ExpressionBase) Copy() *ExpressionBase {
	ec := ExpressionBase{
		BaseElement: e.BaseElement,
		language:    e.language,
		body:        e.body,
		etype:       e.etype,
		retType:     e.retType}
	ec.id = NewID()

	return &ec
}
