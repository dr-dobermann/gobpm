package events

import (
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// CompensationEventDefinition is used in the context of triggering or handling
// compensation. There are four variations: a Start Event, both a catch
// and throw Intermediate Event, and an End Event.
//   - The Compensation Start Event MAY NOT be used for a top-level Process.
//   - The Compensation Start Event MAY be used for an Event Sub-Process.
//   - The catch Compensation Intermediate Event MUST only be attached to the
//     boundary of an Activity and, thus, MAY NOT be used in normal flow.
//   - The throw Compensation Intermediate Event MAY be used in normal flow.
//   - The Compensation End Event MAY be used within any Sub-Process or Process.
type CompensationEventDefinition struct {
	acitivity flow.ActivityNode
	definition
	waitForCompensation bool
}

// Type implements the Definition interface.
func (*CompensationEventDefinition) Type() flow.EventTrigger {
	return flow.TriggerCompensation
}

// NewCompensationEventDefinition creates a new CompensationEventDefinition
// and reterns its pointer.
func NewCompensationEventDefinition(
	activity flow.ActivityNode,
	wait4compensation bool,
	baseOpts ...options.Option,
) (*CompensationEventDefinition, error) {
	d, err := newDefinition(baseOpts...)
	if err != nil {
		return nil, err
	}

	return &CompensationEventDefinition{
		definition:          *d,
		acitivity:           activity,
		waitForCompensation: wait4compensation,
	}, nil
}
