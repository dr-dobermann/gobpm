package process

import (
	"github.com/dr-dobermann/gobpm/pkg/model/common"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

type Process struct {
	common.CallableElement
	flow.ElementsContainer

	// Modeler-defined properties MAY be added to a Process. These properties
	// are contained within the Process. All Tasks and Sub-Processes SHALL have
	// access to these properties.
	Properties []*data.Property

	// correlationSubscriptions are a feature of context-based correlation.
	// CorrelationSubscriptions are used to correlate incoming Messages against
	// data in the Process context. A Process MAY contain several
	// correlationSubscriptions.
	CorrelationSubscriptions []*common.CorrelationSubscription
}

// NewProcess creates a new Process and returns its pointer.
func NewProcess(
	id, name string,
	docs ...*foundation.Documentation,
) *Process {
	return &Process{
		CallableElement:          *common.NewCallableElement(id, name, docs...),
		ElementsContainer:        *flow.NewContainer(id, docs...),
		Properties:               []*data.Property{},
		CorrelationSubscriptions: []*common.CorrelationSubscription{},
	}
}
