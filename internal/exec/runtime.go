package exec

import "github.com/dr-dobermann/gobpm/pkg/model/flow"

// RuntimeEnvironment keeps current runtime environment for the running Instance
// and its tracks
type RuntimeEnvironment interface {
	Scope

	InstanceId() string
}

// ProcessRunner registers process snapshot for latter run on initial event
// firing.
type ProcessRunner interface {
	// RegisterProcess registers a process snapshot to start Instances on
	// initial event firing
	RegisterProcess(*Snapshot) error

	// StartProcess runs process with processId without any event even if
	// process awaits them.
	StartProcess(processId string) error

	// ProcessEvent processes single eventDefinition and if there is any
	// registration of event definition with eDef ID, it starts a new Instance
	// or send the event to runned Instance.
	ProcessEvent(eDef flow.EventDefinition) error
}
