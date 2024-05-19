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
	ReceiveTask TaskType = "ReceiveTask"
	ScriptTask  TaskType = "ScriptTask"
	SendTask    TaskType = "SendTask"
	ServiceTask TaskType = "ServiceTask"
	UserTask    TaskType = "UserTask"
)

type Task interface {
	ActivityNode

	// TaskType returns a type of the Task.
	TaskType() TaskType
}
