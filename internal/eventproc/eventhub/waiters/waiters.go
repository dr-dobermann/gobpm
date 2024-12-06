package waiters

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// CreateWaiter creates a new eventWaiter with given EventDefinition and
// EventProcessor.
func CreateWaiter(
	ep eventproc.EventProcessor,
	eDef flow.EventDefinition,
) (eventproc.EventWaiter, error) {
	var (
		w   eventproc.EventWaiter
		err error
	)

	switch eDef.Type() {
	case flow.TriggerTimer:
		w, err = NewTimeWaiter(ep, eDef, "")

	default:
		err = fmt.Errorf(
			"couldn't find builder for eventDefintion #%s of type %s",
			eDef.Id(), eDef.Type())
	}

	return w, err
}
