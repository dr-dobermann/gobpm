package interactor

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/expression"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	hi "github.com/dr-dobermann/gobpm/pkg/model/hinteraction"
)

// HumanTask is the capability a UserTask node exposes to the engine so the loop
// can recognize it as a task that must park for human completion, announce it,
// authorize an acting Actor, and validate submitted outputs (ADR-020). The
// engine type-asserts a node to HumanTask; the node also implements
// eventproc.EventProcessor to receive the completion.
type HumanTask interface {
	foundation.Identifyer

	// Authorize reports whether actor may read/complete the task, resolving the
	// task's assignment triad against src via eng (ADR-020 §2.5). A nil error
	// means authorized; a non-nil error is a non-terminal denial.
	Authorize(
		ctx context.Context,
		actor hi.Actor,
		src data.Source,
		eng expression.Engine,
	) error

	// ValidateOutputs checks submitted outputs against the task's output spec.
	ValidateOutputs(outputs []data.Data) error

	// Renderers returns the task's form/field descriptions (for a TaskView).
	Renderers() []hi.Renderer

	// Roles returns the task's declared resource roles (for a TaskInfo).
	Roles() []*hi.ResourceRole

	// Outputs returns the task's output specification.
	Outputs() []*bpmncommon.ResourceParameter

	// Properties returns the task's properties (e.g. a FORM_ID convention),
	// carried self-describing in a TaskView's data.
	Properties() []*data.Property
}
