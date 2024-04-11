package exec

type RuntimeEnvironment interface {
	Scope

	InstanceId() string
}
