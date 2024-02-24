package events

import "github.com/dr-dobermann/gobpm/pkg/model/options"

// Cancel Events are only used in the context of modeling Transaction
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
func (*CancelEventDefinition) Type() Trigger {

	return TriggerCancel
}

// NewCancelEventDefinition creates a new CancelEventDefinition and returns
// its pointer.
func NewCancelEventDefinition(
	id string,
	baseOpts ...options.Option,
) *CancelEventDefinition {

	return &CancelEventDefinition{
		definition: *newDefinition(baseOpts...),
	}
}