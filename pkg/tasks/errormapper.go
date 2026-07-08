package tasks

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/expression"
)

// Fault is a worker's raw (unclassified) terminal fault (ADR-021 §2.6, SRD-037).
// Code is a protocol/domain status (an HTTP status once a remote transport
// exists, ADR-004); Body is the response payload; Cause is the diagnostic Go
// error. The engine ErrorMapper classifies {Code, Body}; an all-empty Fault
// (only Cause) matches no rule and falls through to the default technical outcome.
type Fault struct {
	Body  *data.ItemDefinition
	Cause error
	Code  string
}

// MappedOutcome is the classification an ErrorMapper yields for a raw fault: one
// of BpmnError (business, interrupting), Status (business, non-interrupting), or
// Technical (retry/terminal). It is a sealed interface — an unexported marker,
// mirroring options.Option — so the set of outcomes is closed to this package.
type MappedOutcome interface {
	mappedOutcome()
}

// BpmnError yields a Business Error: the engine raises Code as a BPMN error
// (interrupting), caught by a matching Error boundary event (ADR-018). Message is
// an optional diagnostic.
type BpmnError struct {
	Code    string
	Message string
}

// Status yields a Business Status: the engine writes Value to the ServiceTask's
// WithStatus variable and the task completes normally.
type Status struct {
	Value data.Value
}

// Technical yields a technical fault: it feeds the retry policy (SRD-038) and is
// terminal for now (SRD-037). It is also the implicit default when no rule matches.
type Technical struct{}

func (BpmnError) mappedOutcome() {}
func (Status) mappedOutcome()    {}
func (Technical) mappedOutcome() {}

// Rule is one ErrorMapper classification rule (first match wins). Code is an exact
// code match ("" matches any code); BodyClause is an optional predicate over the
// fault's {code, body} (nil = code-only); Yield is the outcome on a match.
type Rule struct {
	BodyClause data.FormalExpression
	Yield      MappedOutcome
	Code       string
}

// ErrorMapper classifies a raw worker Fault into a MappedOutcome. The declarative
// RuleMapper covers the common cases; a custom implementation covers imperative
// ones the rule list can't express (ADR-021 §2.6). It is evaluated at resume with
// the execution's expression engine (SRD-037 §4.1).
type ErrorMapper interface {
	Classify(ctx context.Context, ee expression.Engine, f Fault) (MappedOutcome, error)
}

// RuleMapper is the declarative ErrorMapper: an ordered rule list, first match
// wins, falling through to Technical when none match.
type RuleMapper struct {
	rules []Rule
}

// NewRuleMapper builds a declarative ErrorMapper from rules (evaluated in order). A
// rule with a nil Yield is rejected — every rule must classify to some outcome.
func NewRuleMapper(rules ...Rule) (*RuleMapper, error) {
	for i, r := range rules {
		if r.Yield == nil {
			return nil, errs.New(
				errs.M("NewRuleMapper: rule %d has a nil Yield", i),
				errs.C(errorClass, errs.EmptyNotAllowed))
		}
	}

	return &RuleMapper{rules: rules}, nil
}

// Classify returns the first rule's Yield whose code matches and whose BodyClause
// (if any) evaluates true over the fault's {code, body}; no match → Technical.
func (m *RuleMapper) Classify(
	ctx context.Context,
	ee expression.Engine,
	f Fault,
) (MappedOutcome, error) {
	src := newFaultSource(f)

	for _, r := range m.rules {
		if r.Code != "" && r.Code != f.Code {
			continue
		}

		if r.BodyClause != nil {
			v, err := ee.Evaluate(ctx, r.BodyClause, src)
			if err != nil {
				return nil, errs.New(
					errs.M("ErrorMapper: body clause evaluation failed"),
					errs.C(errorClass, errs.OperationFailed),
					errs.E(err))
			}

			if match, _ := v.Get(ctx).(bool); !match {
				continue
			}
		}

		return r.Yield, nil
	}

	return Technical{}, nil
}

// faultSource is the transient data.Source that exposes a Fault's code and body to
// a Rule.BodyClause FormalExpression — the same shape a gateway condition reads
// from the runtime environment (SRD-037 §3.3, §4.5; validated against goexpr).
type faultSource struct {
	body *data.ItemDefinition
	code string
}

// newFaultSource wraps a Fault as a data.Source keyed by "code" and "body".
func newFaultSource(f Fault) *faultSource {
	return &faultSource{code: f.Code, body: f.Body}
}

// Find resolves "code" (the fault code as a string datum) and "body" (the fault
// body item); any other name is an ObjectNotFound error.
func (s *faultSource) Find(_ context.Context, name string) (data.Data, error) {
	switch name {
	case "code":
		return data.MustParameter("code",
			data.MustItemAwareElement(
				data.MustItemDefinition(values.NewVariable(s.code)),
				data.ReadyDataState)), nil

	case "body":
		if s.body == nil {
			return nil, errs.New(
				errs.M("faultSource: the fault carries no body"),
				errs.C(errorClass, errs.ObjectNotFound))
		}

		return data.MustParameter("body",
			data.MustItemAwareElement(s.body, data.ReadyDataState)), nil
	}

	return nil, errs.New(
		errs.M("faultSource: no datum %q (only code, body)", name),
		errs.C(errorClass, errs.ObjectNotFound))
}
