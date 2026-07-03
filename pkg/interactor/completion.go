package interactor

import (
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// completionTrigger is the internal sentinel trigger of a TaskCompletion. It is
// never matched against BPMN triggers — the loop delivers a completion straight
// to the parked track, bypassing the EventHub and correlation.
const completionTrigger flow.EventTrigger = "UserTaskCompletion"

// TaskCompletion is the synthetic event a completed UserTask rides back into the
// instance loop: it implements flow.EventDefinition, so it flows through the
// parked track's event channel exactly like a message payload, and it carries
// the validated outputs the UserTask binds to scope on resume (ADR-020 §2.1). It
// never reaches the EventHub or correlation, so its Type is an internal sentinel
// and it exposes no ItemDefinitions — the outputs travel via Outputs.
type TaskCompletion struct {
	foundation.BaseElement
	outputs []data.Data
}

// NewTaskCompletion builds a completion event carrying the validated outputs. Its
// base element (a fresh id, unused for routing) cannot fail to build, so the
// constructor does not return an error.
func NewTaskCompletion(outputs []data.Data) *TaskCompletion {
	return &TaskCompletion{
		BaseElement: *foundation.MustBaseElement(),
		outputs:     append([]data.Data{}, outputs...),
	}
}

// Type returns the internal completion sentinel.
func (c *TaskCompletion) Type() flow.EventTrigger {
	return completionTrigger
}

// GetItemsList returns nil — a completion carries its data via Outputs, not
// ItemDefinitions.
func (c *TaskCompletion) GetItemsList() []*data.ItemDefinition {
	return nil
}

// Outputs returns the validated outputs the UserTask binds to scope on resume.
func (c *TaskCompletion) Outputs() []data.Data {
	return append([]data.Data{}, c.outputs...)
}

var _ flow.EventDefinition = (*TaskCompletion)(nil)
