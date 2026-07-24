// Package events provides BPMN event implementations.
package events

import (
	"errors"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// CancelEventDefinition is only used in the context of modeling Transaction
// Sub-Processes. There are two variations: a catch Intermediate Event and an
// End Event.
//   - The catch Cancel Intermediate Event MUST only be attached to the
//     boundary of a Transaction Sub-Process and, thus, MAY NOT be used in
//     normal flow.
//   - The Cancel End Event MUST only be used within a Transaction Sub-Process
//     and, thus, MAY NOT be used in any other type of Sub-Process or Process.
type CancelEventDefinition struct {
	definition
}

// Type implements the Definition interface.
func (*CancelEventDefinition) Type() flow.EventTrigger {
	return flow.TriggerCancel
}

// NewCancelEventDefinition creates a new CancelEventDefinition and returns
// its pointer.
func NewCancelEventDefinition(
	baseOpts ...options.Option,
) (*CancelEventDefinition, error) {
	d, err := newDefinition(baseOpts...)
	if err != nil {
		return nil, err
	}

	return &CancelEventDefinition{
		definition: *d,
	}, nil
}

// ValidateCancelEndPlacement rejects a Cancel End Event that is not directly
// inside a Transaction Sub-Process (BPMN §10.7 — "MAY NOT be used in any other
// type of Sub-Process or Process"; ADR-028 §2.6). `isTransaction` is true only
// for a Transaction container's own nodes; every other container — a plain
// Sub-Process, the top-level Process — passes false. The rule is local: each
// container checks its DIRECT children, so no whole-graph walk is needed
// (a nested container validates its own nodes through its own hook).
func ValidateCancelEndPlacement(nodes []flow.Node, isTransaction bool) error {
	if isTransaction {
		return nil
	}

	ee := []error{}

	for _, n := range nodes {
		if !isCancelEndEvent(n) {
			continue
		}

		ee = append(ee, errs.New(
			errs.M("a Cancel End Event is only allowed directly inside a "+
				"Transaction Sub-Process (BPMN §10.7, ADR-028 §2.6)"),
			errs.C(errorClass, errs.InvalidObject),
			errs.D("end_event_id", n.ID())))
	}

	if len(ee) > 0 {
		return errors.Join(ee...)
	}

	return nil
}

// isCancelEndEvent reports whether n is an End Event carrying a Cancel trigger —
// the abort trigger legal only inside a Transaction (ADR-028 §2.6).
func isCancelEndEvent(n flow.Node) bool {
	en, ok := n.(flow.EventNode)
	if !ok || en.EventClass() != flow.EndEventClass {
		return false
	}

	for _, d := range en.Definitions() {
		if d.Type() == flow.TriggerCancel {
			return true
		}
	}

	return false
}
