package exec

import "github.com/dr-dobermann/gobpm/pkg/model/flow"

// Runner runs single process instance.
type ProcessRunner interface {
	// Run starts Instance with list(probalby empty) of values of initial event
	// definition.
	// If process has no dedicated start event, then flow.Node will be empty.
	RunProcess(*Snapshot, flow.Node, ...flow.EventDefinition) error
}
