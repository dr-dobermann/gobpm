package tasks

import (
	"context"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/expression"
)

// OutputRule extracts a value from a Complete's raw response body into a named
// output variable (ADR-021 §2.5, SRD-037 FR-7). Path is a body-path expression
// (in-process binding: gobpm expressions over Go values, reading the body as the
// "body" datum); Var is the output variable it fills — a plain name ("orderId")
// or a structural path ("order.items[0].price", ADR-011 v.6 §2.9.3) that shapes
// one nested output value; Required makes a path the body doesn't satisfy a fault
// rather than a skipped mapping.
type OutputRule struct {
	Path     data.FormalExpression
	Var      string
	Required bool
}

// outputHead groups the rules that share a Var head (the name before any
// structural step), preserving their order. hasPlain records whether any rule
// addresses the head as a whole (no structural steps).
type outputHead struct {
	rules    []OutputRule
	hasPlain bool
}

// ApplyOutputMapping evaluates each rule's Path over body and returns the extracted
// data to commit as the ServiceTask's output, one datum per Var head. A plain Var
// ("orderId") emits its evaluated value whole; a set of structural Vars sharing a
// head ("order.total", "order.items[0].price") assembles ONE record for that head
// via values.SetPath (ADR-011 v.6 §2.9.3, §2.9.5). Mixing a whole-value and a
// structural rule on one head, or a malformed Var path, is a classified mapping
// error; a required path that fails to evaluate is a fault (a worker response that
// violates the contract → technical fault), an optional one is skipped. The body
// is exposed to Path as the "body" datum through the same transient Source the
// ErrorMapper classifier uses (SRD-037 §4.5).
func ApplyOutputMapping(
	ctx context.Context,
	ee expression.Engine,
	rules []OutputRule,
	body *data.ItemDefinition,
) ([]data.Data, error) {
	order, heads, err := groupByHead(rules)
	if err != nil {
		return nil, err
	}

	src := newFaultSource(Fault{Body: body})

	out := make([]data.Data, 0, len(order))

	for _, head := range order {
		datum, ok, err := assembleHead(ctx, ee, src, head, heads[head])
		if err != nil {
			return nil, err
		}

		if ok {
			out = append(out, datum)
		}
	}

	return out, nil
}

// groupByHead buckets rules by their Var head, keeping first-seen head order for
// a deterministic output. A malformed Var path surfaces SplitPath's classified
// error.
func groupByHead(
	rules []OutputRule,
) (order []string, heads map[string]*outputHead, err error) {
	heads = make(map[string]*outputHead, len(rules))

	for _, r := range rules {
		head, steps, splitErr := data.SplitPath(r.Var)
		if splitErr != nil {
			return nil, nil, splitErr
		}

		h := heads[head]
		if h == nil {
			h = &outputHead{}
			heads[head] = h
			order = append(order, head)
		}

		h.rules = append(h.rules, r)
		if len(steps) == 0 {
			h.hasPlain = true
		}
	}

	return order, heads, nil
}

// assembleHead produces the single datum for one head: the evaluated whole value
// for a plain head, or a record assembled from every structural rule. ok is false
// when the head yields nothing (an optional rule that failed to evaluate). A head
// mixing a whole-value and a structural rule is a classified error.
func assembleHead(
	ctx context.Context,
	ee expression.Engine,
	src data.Source,
	head string,
	h *outputHead,
) (data.Data, bool, error) {
	if h.hasPlain && len(h.rules) > 1 {
		return nil, false, errs.New(
			errs.M("output mapping: head %q mixes a whole-value and a "+
				"structural rule", head),
			errs.C(errorClass, errs.OperationFailed))
	}

	if h.hasPlain {
		v, ok, err := evalRule(ctx, ee, src, h.rules[0])
		if err != nil || !ok {
			return nil, false, err
		}

		d, err := outputDatum(head, v)
		if err != nil {
			return nil, false, err
		}

		return d, true, nil
	}

	rec := values.MustRecord() // zero fields → never errors
	wrote := false

	for _, r := range h.rules {
		v, ok, err := evalRule(ctx, ee, src, r)
		if err != nil {
			return nil, false, err
		}

		if !ok {
			continue
		}

		below := strings.TrimPrefix(r.Var[len(head):], ".")
		if err := values.SetPath(ctx, rec, below, v); err != nil {
			return nil, false, errs.New(
				errs.M("output mapping: cannot set %q", r.Var),
				errs.C(errorClass, errs.OperationFailed),
				errs.E(err))
		}

		wrote = true
	}

	if !wrote {
		return nil, false, nil
	}

	d, err := outputDatum(head, rec)
	if err != nil {
		return nil, false, err
	}

	return d, true, nil
}

// evalRule evaluates one rule's Path over the body source. ok is false when an
// optional path fails to evaluate (skipped); a required failure is an error.
func evalRule(
	ctx context.Context,
	ee expression.Engine,
	src data.Source,
	r OutputRule,
) (data.Value, bool, error) {
	v, err := ee.Evaluate(ctx, r.Path, src)
	if err != nil {
		if r.Required {
			return nil, false, errs.New(
				errs.M("output mapping: required path for %q not satisfied",
					r.Var),
				errs.C(errorClass, errs.OperationFailed),
				errs.E(err))
		}

		return nil, false, nil
	}

	return v, true, nil
}

// outputDatum wraps an assembled value as a Ready, named output datum. It
// errors instead of panicking on a bad name (FIX-026 — mapped output names
// come from worker-supplied policy data).
func outputDatum(name string, v data.Value) (data.Data, error) {
	datum, err := data.ReadyValueParameter(name, v)
	if err != nil {
		return nil, outputDatumErr(name, err)
	}

	return datum, nil
}

// outputDatumErr classifies an output-datum build failure.
func outputDatumErr(name string, err error) error {
	return errs.New(
		errs.M("couldn't build mapped output datum %q", name),
		errs.C(errorClass, errs.OperationFailed),
		errs.E(err))
}
