package model

type ExpressionType uint8

const (
	ExTEmbedded ExpressionType = iota
)

type Expression struct {
	NamedElement
	language   string // Formal Expression language (FEEL) in URI format
	body       string
	etype      ExpressionType
	retType    VarType
	calculated bool
}

func (e Expression) Type() ExpressionType {
	return e.etype
}

func (e *Expression) Copy() *Expression {
	ec := Expression{
		NamedElement: e.NamedElement,
		language:     e.language,
		body:         e.body,
		etype:        e.etype,
		retType:      e.retType}
	ec.id = NewID()

	return &ec
}
