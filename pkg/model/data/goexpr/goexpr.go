/*
GoExpression package is a reference implementation of
common.FormalExpression interface to support go function
as FormalExpression evaluation core.
*/

package goexpr

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

const (
	errorClass = "GOBPM_EXPRESSION_ERRORS"

	language = "gobpm:goexpr"
)

// GExpFunc incapsulated the functional logic of the GoBpmExpression.
type GExpFunc func(ctx context.Context, ds data.Source) (data.Value, error)

// GExpression implements the common.FormalInterface.
// It based on simple go function.
type GExpression struct {
	data.Expression

	src data.Source

	result *data.ItemDefinition

	gexFunc GExpFunc

	evaluated bool
}

// New creates a new GoBpmExpression.
// ds could be nil, but on evaluation it should be set.
func New(
	ds data.Source,
	res *data.ItemDefinition,
	gfunc GExpFunc,
	opts ...options.Option,
) (*GExpression, error) {
	if res == nil || gfunc == nil {
		return nil,
			errs.New(
				errs.M("data source, result, gfunc shouldn't be empty"),
				errs.C(errorClass, errs.InvalidParameter),
				errs.D("data_source", ds),
				errs.D("result", res),
				errs.D("gex_func", gfunc))
	}

	exp, err := data.NewExpression(opts...)
	if err != nil {
		return nil,
			errs.New(
				errs.M("couldn't create an Expression"),
				errs.C(errorClass, errs.BulidingFailed),
				errs.E(err))
	}

	return &GExpression{
			Expression: *exp,
			src:        ds,
			result:     res,
			gexFunc:    gfunc,
		},
		nil
}

// Must tries to create a GExpression variabla and panics on failure.
func Must(
	ds data.Source,
	res *data.ItemDefinition,
	gfunc GExpFunc,
	opts ...options.Option,
) *GExpression {
	ge, err := New(ds, res, gfunc, opts...)
	if err != nil {
		errs.Panic("couldn't create a GExpression: " + err.Error())

		return nil
	}

	return ge
}

// --------------- common.FormalExpression interface --------------------------

// Language returns the FormalExpression language in URI format.
func (ge *GExpression) Language() string {
	return language
}

// Evaluate evaluate the expression and returns its result.
func (ge *GExpression) Evaluate(
	ctx context.Context,
	source data.Source,
) (data.Value, error) {
	ge.evaluated = false

	if ge.gexFunc == nil {
		return nil,
			errs.New(
				errs.M("gex_func is empty. GExpression wasn't created properly"),
				errs.C(errorClass, errs.InvalidState))
	}

	src := ge.src

	if source != nil {
		src = source
	}

	if src == nil {
		return nil,
			errs.New(
				errs.M("no source"),
				errs.C(errorClass, errs.InvalidState))
	}

	res, err := ge.gexFunc(ctx, src)
	if err != nil {
		return nil,
			errs.New(
				errs.M("evaluatuion failed"),
				errs.C(errorClass, errs.OperationFailed),
				errs.E(err))
	}

	if err := ge.result.Structure().Update(res.Get()); err != nil {
		return nil,
			errs.New(
				errs.M("result value updating failed"),
				errs.C(errorClass, errs.OperationFailed),
				errs.E(err))
	}

	ge.evaluated = true

	return ge.result.Structure(), nil
}

// Result returns evaluated result of the formal expression.
// If there is no evaluation was made, an error returned.
func (ge *GExpression) Result() (data.Value, error) {
	if !ge.evaluated {
		return nil,
			errs.New(
				errs.M("evaluation wasn't made. result isn't accessible"),
				errs.C(errorClass, errs.InvalidState))
	}

	return ge.result.Structure(), nil
}

// ResultType returns name of the FormalExpression result type.
func (ge *GExpression) ResultType() string {
	return ge.result.Structure().Type()
}

// IsEvaluated returns true if result is ready.
func (ge *GExpression) IsEvaluated() bool {
	return ge.evaluated
}

// ----------------------------------------------------------------------------

// ----------------------------------------------------------------------------
// interface check
var (
	_ data.FormalExpression = (*GExpression)(nil)
)
