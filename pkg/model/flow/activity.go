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
	ScriptTask  TaskType = "ScriptTask"
	SendTask    TaskType = "SendTask"
	ServiceTask TaskType = "ServiceTask"
	UserTask    TaskType = "UserTask"
)

type Task interface {
	ActivityNode

	TaskType() TaskType
}
