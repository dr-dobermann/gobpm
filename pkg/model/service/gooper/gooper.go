/*
#goOper implements service.Implementor interface.

goOper creates an Operation Implementor which could use go functor
as Operation execution mechanism.
*/

package gooper

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
)

const (
	errorClass = "GOOPER"

	GoOperType = "##GoOper"
)

// OpFunctior is an Operation Implementor which
type OpFunctor func(*data.ItemDefinition) (*data.ItemDefinition, error)

type GoFunc struct {
	ers []string
	f   OpFunctor
}

// New creates a new GoFunc based on OpFunctor f and list of error classes,
// the OpFunctor could return.
// It returns a pointer on a new GoFunc or error on failure.
func New(
	f OpFunctor,
	ers ...string,
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
	return GoOperType
}

// ErrorClasses returns errors classes list which may be
// returned by the Execute call.
func (gf GoFunc) ErrorClasses() []string {
	return append([]string{}, gf.ers...)
}

// Execute runs an operation implementator with in parameter and
// returns the output result (couldn be nil) and error status.
func (gf GoFunc) Execute(
	ctx context.Context,
	in *data.ItemDefinition,
) (*data.ItemDefinition, error) {
	out, err := gf.f(in)
	if err != nil {
		return nil, errs.New(
			errs.M("goOper failed"),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err))
	}

	return out, nil
}

//------------------------------------------------------------------------------

// interface check
var _ service.Implementor = (*GoFunc)(nil)
