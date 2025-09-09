package waiters

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// CreateWaiter creates a new eventWaiter with given EventDefinition and
// EventProcessor.
func CreateWaiter(
	eh eventproc.EventHub,
	ep eventproc.EventProcessor,
	eDef flow.EventDefinition,
) (eventproc.EventWaiter, error) {
	if eh == nil {
		return nil, fmt.Errorf("empty event hub")
	}

	if ep == nil {
		return nil, fmt.Errorf("empty event processor")
	}

	if eDef == nil {
		return nil, fmt.Errorf("empty event definition")
	}

	var (
		w   eventproc.EventWaiter
		err error
	)

	switch eDef.Type() {
	case flow.TriggerTimer:
		w, err = NewTimeWaiter(eh, ep, eDef, "")

	default:
		err = fmt.Errorf(
			"couldn't find builder for eventDefintion #%s of type %s",
			eDef.ID(), eDef.Type())
	}

	return w, err
}
