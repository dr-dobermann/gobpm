package initiator

import (
	"github.com/dr-dobermann/gobpm/internal/exec"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// Snapshot holds process'es snapshot ready to run.
type Snapshot struct {
	foundation.BaseElement

	ProcessId string
	Nodes     map[string]exec.NodeExecutor
	Flows     map[string]*flow.SequenceFlow
}

// NewSnapshot creates a new snapshot from the Process p and returns its
// pointer on success or error on failure.
func NewSnapshot(p *process.Process) (*Snapshot, error) {
	return nil, nil
}
