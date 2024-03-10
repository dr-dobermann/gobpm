package process

import (
	"github.com/dr-dobermann/gobpm/pkg/model/common"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
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
	name string,
	baseOpts ...options.Option,
) *Process {
	ec := flow.NewContainer(baseOpts...)

	// CallableElement should have same Id as ElementsContainer Id if
	// id isn't provided.
	if len(baseOpts) == 0 {
		baseOpts = append(baseOpts, foundation.WithId(ec.Id()))
	}

	return &Process{
		CallableElement:          *common.NewCallableElement(name, baseOpts...),
		ElementsContainer:        *ec,
		Properties:               []*data.Property{},
		CorrelationSubscriptions: []*common.CorrelationSubscription{},
	}
}
