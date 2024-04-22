package exec

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
}
