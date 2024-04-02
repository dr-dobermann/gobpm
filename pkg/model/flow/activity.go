package flow

type ActivityType string

const (
	TaskActivity       ActivityType = "Task"
	CallActivity       ActivityType = "CallActivity"
	SubProcessActivity ActivityType = "SubProcess"
)

type ActivityNode interface {
	Node

	ActivityType() ActivityType
}

type TaskType string

const (
	RecieveTask TaskType = "RecieveTask"
	SendTask    TaskType = "SendTask"
	ServiceTask TaskType = "ServiceTask"
)

type Task interface {
	ActivityNode

	TaskType() TaskType
}
