// Package interactor defines the human-task boundary between the engine and an
// embedder (ADR-020): the pluggable TaskDistributor the engine announces parked
// UserTasks to, the TaskInfo/TaskView it hands across that boundary, the
// TaskCompletion event a completed task rides back on, and the HumanTask
// capability a UserTask node exposes so the engine can authorize and validate it.
package interactor
