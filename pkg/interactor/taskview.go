package interactor

import (
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	hi "github.com/dr-dobermann/gobpm/pkg/model/hinteraction"
)

// TaskRef identifies a parked human task across the engine boundary. It is
// embedded in both the pre-authorization announcement (TaskInfo) and the
// post-authorization snapshot (TaskView), so the identity is declared once
// (ADR-020 §2.8).
type TaskRef struct {
	TaskID     string
	InstanceID string
	NodeID     string
	ProcessID  string
}

// TaskInfo is the announcement handed to a TaskDistributor when a UserTask
// becomes available — before any authorization — so it carries identity plus
// the roles that may claim it (for inbox routing/filtering) and deliberately
// NO task data: instance variables must not reach the distributor before an
// authorized Take (ADR-020 §2.8).
type TaskInfo struct {
	TaskRef
	Roles []*hi.ResourceRole
}

// TaskView is the authorized snapshot returned by Take: the renderers to build
// the UI and the self-describing data (task inputs plus properties such as a
// FORM_ID convention). It is produced only after the acting Actor passes
// authorization, so — unlike TaskInfo — it carries the task's data.
type TaskView struct {
	TaskRef
	Renderers []hi.Renderer
	Data      []data.Data
}
