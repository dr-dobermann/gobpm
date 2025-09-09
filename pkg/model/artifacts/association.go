package artifacts

import "github.com/dr-dobermann/gobpm/pkg/model/foundation"

// AssociationDirection defines the direction of an association.
type AssociationDirection string

const (
	// None represents no association direction.
	None AssociationDirection = "None"
	// One represents one-way association direction.
	One  AssociationDirection = "One"
	// Both represents bi-directional association direction.
	Both AssociationDirection = "Both"
)

// An Association is used to associate information and Artifacts with Flow
// Objects. Text and graphical non-Flow Objects can be associated with the Flow
// Objects and Flow. An Association is also used to show the Activity used for
// compensation.
type Association struct {
	foundation.BaseElement

	// Direction is an attribute that defines whether or not the Association
	// shows any directionality with an arrowhead. The default is None (no
	// arrowhead). A value of One means that the arrowhead SHALL be at the
	// Target Object. A value of Both means that there SHALL be an arrowhead at
	// both ends of the Association line.
	Direction AssociationDirection

	// The BaseElement that the Association is connecting from.
	Source *foundation.BaseElement

	// The BaseElement that the Association is connecting to.
	Target *foundation.BaseElement
}
