/*
#goOper implements service.Implementor interface.

goOper creates an Operation Implementor which could use go functor
as Operation execution mechanism.
*/

package gooper

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
)

const errorClass = "GOOPER"

// OpFunctior is an Operation Implementor which
type OpFunctor func(*data.ItemDefinition) (*data.ItemDefinition, error)

type GoFunc struct {
	ers []string
	f   OpFunctor
}

func New(
	ers []string,
	f OpFunctor,
) (service.Implementor, error) {
	if f == nil {
		return nil,
			errs.New(
				errs.M("empty functor"),
				errs.C(errorClass, errs.EmptyNotAllowed))
	}

	return &GoFunc{
		ers: ers,
		f:   f,
	}, nil
}

// ------------------------ service.Implementor interface ----------------------

// Type returns type of the executor.
func (gf GoFunc) Type() string {
	return "##GoOper"
}

// ErrorClasses returns errors classes list which may be
// returned by the Execute call.
func (gf GoFunc) ErrorClasses() []string {
	return append([]string{}, gf.ers...)
}

// Execute runs an operation with all parameters provided by
// Operation entity.
func (gf GoFunc) Execute(op *service.Operation) error {
	var in *data.ItemDefinition

	im := op.IncomingMessage()
	if im != nil {
		in = im.Item()
	}

	out, err := gf.f(in)
	if err != nil {
		return errs.New(
			errs.M("operation #%s execution failed", op.Id()),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err))
	}

	om := op.OutgoingMessage()

	switch {
	case out != nil && om == nil:
		return errs.New(
			errs.M("no out message for not empty GoFunc"),
			errs.C(errorClass, errs.OperationFailed))

	case out == nil && om != nil:
		return errs.New(
			errs.M("empty GoFunc result with not empty outMessage"),
			errs.C(errorClass, errs.EmptyNotAllowed))

	case out != nil && om != nil:
		return om.Item().Structure().Update(out.Structure().Get())
	}

	return nil
}

//------------------------------------------------------------------------------
