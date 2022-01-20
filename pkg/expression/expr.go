package expression

import (
	"fmt"
	"io"
	"io/ioutil"
	"strings"

	"github.com/dr-dobermann/gobpm/internal/errs"
	"github.com/dr-dobermann/gobpm/model/base"
	"github.com/dr-dobermann/gobpm/pkg/identity"
	"github.com/dr-dobermann/gobpm/pkg/variables"
	vars "github.com/dr-dobermann/gobpm/pkg/variables"
)

const (
	InvVariable = "InvalidVariable"
)

type Expression interface {
	ID() identity.Id
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

	language string // could be Formal Expression language (FEEL) in URI format
	body     []byte

	state      ExpressionState
	parameters map[string]vars.Variable
	retType    vars.Type
}

func New(
	id identity.Id,
	lang string,
	rt vars.Type) *FormalExpression {

	return &FormalExpression{
		BaseElement: *base.New(id),
		language:    strings.Trim(lang, " "),
		body:        []byte{},
		retType:     rt}
}

func (e *FormalExpression) Language() string {
	return e.language
}

func (e *FormalExpression) GetBody() []byte {
	if len(e.body) == 0 {
		return []byte{}
	}

	bc := make([]byte, len(e.body))
	copy(bc, e.body)

	return bc
}

func (e *FormalExpression) UpdateBody(src io.Reader) error {
	if src == nil {
		return e.NewExprErr(nil, "new body is empty")
	}

	buf, err := ioutil.ReadAll(src)
	if err != nil {
		return e.NewExprErr(err, "couldn't update expression body")
	}

	e.body = buf
	e.state = Created

	return nil
}

func (e *FormalExpression) State() ExpressionState {
	return e.state
}

func (e *FormalExpression) Params() []vars.Variable {
	pl := []vars.Variable{}

	if e.parameters != nil {
		for _, p := range e.parameters {
			pl = append(pl, p)
		}
	}

	return pl
}

func (e *FormalExpression) SetParams(vars ...vars.Variable) error {
	params := map[string]variables.Variable{}

	for _, v := range vars {
		// check for correctnes
		if len(strings.Trim(v.Name(), " ")) == 0 {
			return e.NewExprErr(nil, "parameter should have a non-empty name")
		}

		// check for duplication
		if _, ok := e.parameters[v.Name()]; ok {
			return e.NewExprErr(nil, "parameter '%s' already exists", v.Name())
		}

		// add new param
		params[v.Name()] = v
	}

	e.parameters = params
	e.state = Parameterized

	return nil
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
		state:       Created,
		language:    e.language,
		body:        make([]byte, len(e.body)),
		parameters:  map[string]variables.Variable{},
		retType:     e.retType}

	ec.SetNewID(identity.NewID())
	copy(ec.body, e.body)

	for pn, p := range e.parameters {
		ec.parameters[pn] = p.Copy()
	}

	return &ec
}

func (e *FormalExpression) NewExprErr(
	err error,
	format string,
	values ...interface{}) ExpressionError {

	return ExpressionError{
		exprID: e.ID(),
		msg:    fmt.Sprintf(format, values...),
		Err:    err,
	}
}
