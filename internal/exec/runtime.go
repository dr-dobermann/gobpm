package exec

type RuntimeEnvironment interface {
	InstanceId() string
	Scope() Scope
}
