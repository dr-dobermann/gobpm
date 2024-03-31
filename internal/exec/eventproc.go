package exec

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

type EventProcessor interface {
	ProcessEvent(context.Context, flow.EventDefinition) error
}

type EventProducer interface {
	RegisterEvents(EventProcessor, ...flow.EventDefinition) error
}
