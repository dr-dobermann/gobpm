package exec

// RuntimeEnvironment keeps current runtime environment for the running Instance
// and its tracks
type RuntimeEnvironment interface {
	Scope

	// InstanceId returns the process instance Id.
	InstanceId() string

	// EventProducer returns the EventProducer of the runtime.
	EventProducer() EventProducer

	// Scope returns the Scope of the runtime.
	Scope() Scope
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
}
