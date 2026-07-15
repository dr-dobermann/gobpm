// Package goexpr is a reference implementation of
// bpmncommon.FormalExpression interface to support go function
// as FormalExpression evaluation core.
package goexpr

import (
	"context"
	"strconv"

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

// GExpression implements the bpmncommon.FormalInterface.
// It based on simple go function.
type GExpression struct {
	src     data.Source
	result  *data.ItemDefinition
	gexFunc GExpFunc
	// deps are the read paths declared via WithDependencies, or nil when
	// the expression declared nothing (data.DependencyLister).
	deps []string
	data.Expression
	evaluated bool
}

// New creates a new GoBpmExpression.
//
// Parameters:
//   - ds - data.Source used for expression results calculations. Could be nil
//     on construction and might be changed on Evaluation.
//   - res - data.ItemDefinition which will be used as result of evaluation.
//     It is used for set type of expression.
//   - gFunc - func(ctx context.Context, ds data.Source) (data.Value, error) is
//     an evaluation func.
//   - options of the expression.
//
// Available options are:
//   - foundation.WithID
//   - foundation.WithDoc
//   - goexpr.WithDependencies
func New(
	ds data.Source,
	res *data.ItemDefinition,
	gfunc GExpFunc,
	opts ...options.Option,
) (*GExpression, error) {
	if res == nil || gfunc == nil {
		return nil,
			errs.New(
				errs.M("result, gfunc shouldn't be empty"),
				errs.C(errorClass, errs.InvalidParameter),
				errs.D("result_is_nil", strconv.FormatBool(res == nil)),
				errs.D("gfunc_is_nil", strconv.FormatBool(gfunc == nil)))
	}

	// goexpr-local options apply to the GExpression below; every other
	// option forwards to the embedded data.Expression unchanged.
	gexOpts := make([]GExpOption, 0, len(opts))
	expOpts := make([]options.Option, 0, len(opts))

	for _, o := range opts {
		switch opt := o.(type) {
		case GExpOption:
			gexOpts = append(gexOpts, opt)

		default:
			expOpts = append(expOpts, o)
		}
	}

	exp, err := data.NewExpression(expOpts...)
	if err != nil {
		return nil,
			errs.New(
				errs.M("couldn't create an Expression"),
				errs.C(errorClass, errs.BulidingFailed),
				errs.E(err))
	}

	ge := &GExpression{
		Expression: *exp,
		src:        ds,
		result:     res,
		gexFunc:    gfunc,
	}

	for _, o := range gexOpts {
		if err := o(ge); err != nil {
			return nil, err
		}
	}

	return ge, nil
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

// --------------- bpmncommon.FormalExpression interface --------------------------

// Language returns the FormalExpression language in URI format.
func (ge *GExpression) Language() string {
	return language
}

// Evaluate evaluate the expression and returns its result.
// If source isn't empty it substites current ge source.
// If expression demands external data is should check if
// source is nil by itself.
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

	if source != nil {
		ge.src = source
	}

	res, err := ge.gexFunc(ctx, ge.src)
	if err != nil {
		return nil,
			errs.New(
				errs.M("evaluatuion failed"),
				errs.C(errorClass, errs.OperationFailed),
				errs.E(err))
	}

	// A user GExpFunc may legitimately return (nil, nil); reject it as a
	// classified error rather than nil-dereferencing res.Get below (FIX-010).
	if res == nil {
		return nil,
			errs.New(
				errs.M("goexpr: evaluation produced a nil value"),
				errs.C(errorClass, errs.OperationFailed))
	}

	if err := ge.result.Structure().Update(ctx, res.Get(ctx)); err != nil {
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

// Dependencies implements data.DependencyLister: the read paths declared
// via WithDependencies, or nil when the expression declared nothing (= may
// read anything — its conditional subscription re-evaluates on every
// non-empty commit, ADR-006 v.3 §2.7).
func (ge *GExpression) Dependencies() []string {
	return ge.deps
}

// ----------------------------------------------------------------------------
// interface check
var (
	_ data.FormalExpression = (*GExpression)(nil)
	_ data.DependencyLister = (*GExpression)(nil)
)
