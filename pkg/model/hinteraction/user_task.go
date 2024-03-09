package human_interaction

import "github.com/dr-dobermann/gobpm/pkg/model/activities"

// A User Task is a typical “workflow” Task where a human performer performs
// the Task with the assistance of a software application and is scheduled
// through a task list manager of some sort.
type UserTask struct {
	activities.Task

	Implementation string

	Renderings []Rendering
}
