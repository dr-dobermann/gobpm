package events

import (
	"fmt"
	"strconv"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// dirWords names the parameter a definition's payload lands in, per
// direction (the data-over-code rule for a fixed mapping).
var dirWords = map[data.Direction]string{
	data.Input:  "input",
	data.Output: "output",
}

// itemOf returns the item an item-bearing definition carries — a Message,
// Signal, Error or Escalation payload (semantics/data.md p217) — or nil for
// a definition that carries none: a Timer, a Conditional, a Link, and an
// item with no structure, which has no value to carry — "if absent, payload
// does not flow" (p217), so it declares no parameter.
func itemOf(def flow.EventDefinition) *data.ItemDefinition {
	items := def.GetItemsList()
	if len(items) == 0 || items[0].Structure() == nil {
		return nil
	}

	return items[0]
}

// payloadParameter builds the data parameter def's payload lands in, in
// direction dir, Ready over the definition's item. It errors instead of
// panicking when the parameter cannot be built (FIX-026).
func payloadParameter(
	def flow.EventDefinition, dir data.Direction,
) (*data.Parameter, error) {
	item := itemOf(def)

	// Neither constructor refuses a structured item under a Ready state and
	// a non-empty name; the branches are said in the form the coverage gate
	// reads (FIX-026's error-returning shape, kept).
	iae, err := data.NewItemAwareElement(item, data.ReadyDataState)
	if err != nil {
		return nil, payloadParamErr(def, err)
	}

	p, err := data.NewParameter(payloadParamName(def, item, dir), iae)
	if err != nil {
		return nil, payloadParamErr(def, err)
	}

	return p, nil
}

// payloadParamName names the auto-declared parameter: a message keeps the
// name it always had, every other item-bearing kind reads the same way.
func payloadParamName(
	def flow.EventDefinition, item *data.ItemDefinition, dir data.Direction,
) string {
	if med, ok := def.(*MessageEventDefinition); ok {
		return fmt.Sprintf("message %q(%s) %s",
			med.Message().Name(), med.Message().ID(), dirWords[dir])
	}

	return fmt.Sprintf("%s payload(%s) %s", def.Type(), item.ID(), dirWords[dir])
}

// payloadParamErr classifies a payload parameter build failure: a message
// keeps the FIX-026 classifier carrying the message name.
func payloadParamErr(def flow.EventDefinition, err error) error {
	if med, ok := def.(*MessageEventDefinition); ok {
		return msgOutputErr(med, err)
	}

	return errs.New(
		errs.M("couldn't build %s payload parameter", def.Type()),
		errs.C(errorClass, errs.OperationFailed),
		errs.E(err),
		errs.D("event_trigger", string(def.Type())))
}

// autoParameters declares one parameter per item-bearing definition, in
// definition order — the standard's binding rule (p217): the order of the
// definitions and the order of the parameters is the correspondence.
func autoParameters(
	defs []flow.EventDefinition, dir data.Direction,
) ([]*data.Parameter, error) {
	pp := make([]*data.Parameter, 0, len(defs))

	for _, def := range defs {
		if itemOf(def) == nil {
			continue
		}

		p, err := payloadParameter(def, dir)
		if err != nil {
			return nil, err
		}

		pp = append(pp, p)
	}

	return pp, nil
}

// pairDeclared pairs the declared parameters with the auto-declared ones in
// order (p217): a declared parameter replaces the auto parameter at its
// position and must carry the same item; one past the last item-bearing
// definition pairs with nothing and is refused — an event's data comes from
// what triggered it (§10.4.2), a parameter nothing fills is a modeling
// error said at construction (ADR-040 v.2 §2.7). A nil parameter is refused
// naming its index.
func pairDeclared(
	option string, declared, auto []*data.Parameter,
) ([]*data.Parameter, error) {
	if len(declared) > len(auto) {
		return nil, errs.New(
			errs.M("%s: %d parameters declared, the event has %d item-bearing "+
				"definitions — a parameter nothing fills isn't allowed (§10.4.2)",
				option, len(declared), len(auto)),
			errs.C(errorClass, errs.InvalidParameter))
	}

	paired := append([]*data.Parameter{}, auto...)

	for i, p := range declared {
		if p == nil {
			return nil, errs.New(
				errs.M("%s: a nil parameter isn't allowed", option),
				errs.C(errorClass, errs.EmptyNotAllowed),
				errs.D("index", strconv.Itoa(i)))
		}

		want := auto[i].ItemDefinition().ID()
		if got := p.ItemDefinition().ID(); got != want {
			return nil, errs.New(
				errs.M("%s: parameter %q at position %d carries item %q, "+
					"but the definition it pairs with carries %q (§10.4.2 p217)",
					option, p.Name(), i, got, want),
				errs.C(errorClass, errs.InvalidParameter))
		}

		paired[i] = p
	}

	return paired, nil
}

// WithDataOutputs declares a catch event's data outputs — the parameters
// the triggering element's payload lands in and its output associations
// push from (ADR-011 §2.5). They pair with the event's item-bearing
// definitions in order and must carry those definitions' items (p217); a
// definition left undeclared keeps its auto-declared parameter.
func WithDataOutputs(params ...*data.Parameter) CatchOption {
	return func(ce *catchEvent) error {
		pp, err := pairDeclared("WithDataOutputs", params, ce.dataOutputs)
		if err != nil {
			return err
		}

		ce.dataOutputs = pp

		return nil
	}
}

// ThrowOption configures a throw event at construction, the way a
// CatchOption configures a catch.
type ThrowOption func(*throwEvent) error

// Option marks ThrowOption as an options.Option.
func (ThrowOption) Option() {}

// WithDataInputs declares a throw event's data inputs — the parameters its
// input associations fill when it fires and the thrown element is copied
// from (ADR-011 §2.5). They pair with the event's item-bearing definitions
// in order and must carry those definitions' items (p217); a definition
// left undeclared keeps its auto-declared parameter.
func WithDataInputs(params ...*data.Parameter) ThrowOption {
	return func(te *throwEvent) error {
		pp, err := pairDeclared("WithDataInputs", params, te.dataInputs)
		if err != nil {
			return err
		}

		te.dataInputs = pp

		return nil
	}
}
