package runner

import (
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// ProcessRunner registers process snapshot for latter run on initial event
// firing.
type ProcessRunner interface {
	// RegisterProcess registers a process snapshot to start Instances on
	// initial event firing
	RegisterProcess(*process.Process) error

	// StartProcess runs process with processId without any event even if
	// process awaits them.
	StartProcess(processId string) error
}
