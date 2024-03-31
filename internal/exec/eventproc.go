package exec

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

type EventProcessor interface {
	foundation.Identifyer

	ProcessEvent(context.Context, flow.EventDefinition) error
}

type EventProducer interface {
	RegisterEvents(EventProcessor, ...flow.EventDefinition) error
}
