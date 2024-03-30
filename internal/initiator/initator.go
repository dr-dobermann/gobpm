package initiator

import (
	"context"

	"github.com/dr-dobermann/gobpm/internal/exec"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// Process Initator holds the prepared process Snapshot which is used to
// create an process Instance.
// Process Initator also holds a process Initiation Events List and receive
// all event definition from list to start a new process Instance.
type Initiator struct {
	Sshot       *Snapshot
	InitEvents  []events.Definition
	EvtProducer exec.EventProducer
}

// NewInitiator creates a new Initiator and returns its pointer on success
// or error on failure.
func NewInitiator(
	p *process.Process,
	ep exec.EventProducer,
) (*Initiator, error) {
	return nil, nil
}

// ------------------ exec.EventProcessor interface ----------------------------

// Process processes event definition and on success creates a new process
// instance and add send it to run queue.
func (ini *Initiator) ProcessEvent(
	ctx context.Context,
	eDef events.Definition,
) error {
	return nil
}
