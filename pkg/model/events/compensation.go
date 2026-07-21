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
	activity flow.ActivityNode
	definition
	// waitForCompletion carries the spec's waitForCompletion attribute
	// (§10.4.5, default true): true — the throwing token parks until the
	// triggered compensation completes; false — fire-and-forget (SRD-059 FR-5).
	waitForCompletion bool
}

// Type implements the Definition interface.
func (*CompensationEventDefinition) Type() flow.EventTrigger {
	return flow.TriggerCompensation
}

// NewCompensationEventDefinition creates a new CompensationEventDefinition
// and returns its pointer. A nil activity is legal on a THROW definition — it
// means the spec's default target context (§13.5.5: compensate the throw's
// enclosing scope, scope-wide).
func NewCompensationEventDefinition(
	activity flow.ActivityNode,
	waitForCompletion bool,
	baseOpts ...options.Option,
) (*CompensationEventDefinition, error) {
	d, err := newDefinition(baseOpts...)
	if err != nil {
		return nil, err
	}

	return &CompensationEventDefinition{
		definition:        *d,
		activity:          activity,
		waitForCompletion: waitForCompletion,
	}, nil
}

// Activity returns the referenced activity (the spec's activityRef) — the
// specific activity a throw compensates, or nil for the default target
// context (scope-wide compensation).
func (ced *CompensationEventDefinition) Activity() flow.ActivityNode {
	return ced.activity
}

// WaitForCompletion reports whether a throw carrying this definition waits
// for the triggered compensation to complete before continuing (§10.4.5,
// default true).
func (ced *CompensationEventDefinition) WaitForCompletion() bool {
	return ced.waitForCompletion
}
