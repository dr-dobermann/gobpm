package artifacts

import "github.com/dr-dobermann/gobpm/pkg/model/foundation"

// AssociationDirection defines the direction of an association.
type AssociationDirection string

const (
	// None represents no association direction.
	None AssociationDirection = "None"
	// One represents one-way association direction.
	One AssociationDirection = "One"
	// Both represents bi-directional association direction.
	Both AssociationDirection = "Both"
)

// An Association is used to associate information and Artifacts with Flow
// Objects. Text and graphical non-Flow Objects can be associated with the Flow
// Objects and Flow. An Association is also used to show the Activity used for
// compensation.
type Association struct {
	Source    *foundation.BaseElement
	Target    *foundation.BaseElement
	Direction AssociationDirection
	foundation.BaseElement
}
