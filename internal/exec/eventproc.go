package exec

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/model/events"
)

type EventProcessor interface {
	ProcessEvent(context.Context, events.Definition) error
}

type EventProducer interface {
	RegisterEvents(EventProcessor, ...events.Definition) error
}
