// Package flow provides BPMN flow elements and node definitions.
package flow

// ActivityType represents different types of BPMN activities.
type ActivityType string

const (
	// TaskActivity represents a BPMN task activity.
	TaskActivity       ActivityType = "Task"
	// CallActivity represents a BPMN call activity.
	CallActivity       ActivityType = "CallActivity"
	// SubProcessActivity represents a BPMN subprocess activity.
	SubProcessActivity ActivityType = "SubProcess"
)

// ActivityNode represents a BPMN activity node.
type ActivityNode interface {
	Node

	ActivityType() ActivityType
}

// TaskType represents different types of BPMN tasks.
type TaskType string

const (
	// ReceiveTask represents a BPMN receive task.
	ReceiveTask TaskType = "ReceiveTask"
	// ScriptTask represents a BPMN script task.
	ScriptTask  TaskType = "ScriptTask"
	// SendTask represents a BPMN send task.
	SendTask    TaskType = "SendTask"
	// ServiceTask represents a BPMN service task.
	ServiceTask TaskType = "ServiceTask"
	// UserTask represents a BPMN user task.
	UserTask    TaskType = "UserTask"
)

// Task represents a BPMN task interface.
type Task interface {
	ActivityNode

	// TaskType returns a type of the Task.
	TaskType() TaskType
}
