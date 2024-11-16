package waiters

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

type timerWaiter struct{}

func NewTimerWaiter(
	ep eventproc.EventProcessor,
	eDef flow.EventDefinition,
) (eventproc.EventWaiter, error) {
	return nil, fmt.Errorf("not implemented yet")
}
