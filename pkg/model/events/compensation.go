package events

import (
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// Compensation Events are used in the context of triggering or handling
// compensation. There are four variations: a Start Event, both a catch
// and throw Intermediate Event, and an End Event.
//   - The Compensation Start Event MAY NOT be used for a top-level Process.
//   - The Compensation Start Event MAY be used for an Event Sub-Process.
//   - The catch Compensation Intermediate Event MUST only be attached to the
//     boundary of an Activity and, thus, MAY NOT be used in normal flow.
//   - The throw Compensation Intermediate Event MAY be used in normal flow.
//   - The Compensation End Event MAY be used within any Sub-Process or Process.
type CompensationEventDefinition struct {
	definition

	// For a Start Event:
	//   This Event “catches” the compensation for an Event Sub-Process. No
	//   further information is REQUIRED. The Event Sub-Process will provide the
	//   Id necessary to match the Compensation Event with the Event that threw
	//   the compensation, or the compensation will have been a broadcast.
	// For an End Event:
	//   The Activity to be compensated MAY be supplied. If an Activity is not
	//   supplied, then the compensation is broadcast to all completed
	//   Activities in the current Sub-Process (if present), or the entire
	//   Process instance (if at the global level).
	// For an Intermediate Event within normal flow:
	//   The Activity to be compensated MAY be supplied. If an Activity is not
	//   supplied, then the compensation is broadcast to all completed
	//   Activities in the current Sub-Process (if present), or the entire
	//   Process instance (if at the global level). This “throws” the
	//   compensation.
	// For an Intermediate Event attached to the boundary of an Activity:
	//   This Event “catches” the compensation. No further information is
	//   REQUIRED. The Activity the Event is attached to will provide the Id
	//   necessary to match the Compensation Event with the Event that threw
	//   the compensation, or the compensation will have been a broadcast.
	acitivity flow.ActivityNode

	// For a throw Compensation Event, this flag determines whether the throw
	// Intermediate Event waits for the triggered compensation to complete
	// (the default), or just triggers the compensation and immediately
	// continues.
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
