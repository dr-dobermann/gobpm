package human_interaction

import "github.com/dr-dobermann/gobpm/pkg/model/foundation"

// People can be assigned to Activities in various roles (called “generic human
// roles” in WS-HumanTask). BPMN 1.2 traditionally only has the Performer role.
// In addition to supporting the Performer role, BPMN 2.0 defines a specific
// HumanPerformer element allowing specifying more specific human roles as
// specialization of HumanPerformer, such as PotentialOwner.
type HumanPerformer interface {
	foundation.Identifyer
	foundation.Namer

	// ActualRole returns name of the role user used in interaction.
	ActualRole() string
}
