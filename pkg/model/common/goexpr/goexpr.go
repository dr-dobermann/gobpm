package goexpr

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/common"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

const (
	errorClass = "GOBPM_EXPRESSION_ERRORS"

	language = "gobpm:gexpr"
)

// GExpFunc incapsulated the functional logic of the GoBpmExpression.
type GExpFunc func(ds data.Source) (data.Value, error)

// GExpression implements the common.FormalInterface.
// It based on simple go function.
type GExpression struct {
	common.Expression

	src data.Source

	result *data.ItemDefinition

	gexFunc GExpFunc

	evaluated bool
}

// New creates a new GoBpmExpression.
func New(
	ds data.Source,
	res *data.ItemDefinition,
	gfunc GExpFunc,
	opts ...options.Option,
) (*GExpression, error) {
	if ds == nil || res == nil || gfunc == nil {
		return nil,
			errs.New(
				errs.M("data source, result, gfunc shouldn't be empty"),
				errs.C(errorClass, errs.InvalidParameter),
				errs.D("data_source", ds),
				errs.D("result", res),
				errs.D("gex_func", gfunc))
	}

	exp, err := common.NewExpression(opts...)
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

// --------------- common.FormalExpression interface --------------------------

// Language returns the FormalExpression language in URI format.
func (ge *GExpression) Language() string {
	return language
}

// Evaluate evaluate the expression and returns its result.
func (ge *GExpression) Evaluate() (data.Value, error) {
	if ge.gexFunc == nil {
		return nil,
			errs.New(
				errs.M("gex_func is empty. GExpression wasn't created properly"),
				errs.C(errorClass, errs.InvalidObject))
	}

	ge.evaluated = false

	res, err := ge.gexFunc(ge.src)
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
var _ common.FormalExpression = (*GExpression)(nil)
