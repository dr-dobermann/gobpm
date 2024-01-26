package artifacts

import "github.com/dr-dobermann/gobpm/pkg/model/foundation"

// BPMN provides modelers with the capability of showing additional information
// about a Process that is not directly related to the Sequence Flows or Message
// Flows of the Process.
// At this point, BPMN provides three standard Artifacts: Associations, Groups,
// and Text Annotations.
// Additional Artifacts MAY be added to the BPMN International Standard in later
// versions. A modeler or modeling tool MAY extend a BPMN diagram and add new
// types of Artifacts to a Diagram. Any new Artifact MUST follow the Sequence
// Flow and Message Flow connection rules. Associations can be used to link
// Artifacts to Flow Objects.

// *****************************************************************************

type Artifact struct {
	foundation.BaseElement
}

// NewArtifact creates a new Artifact and returns its pointer.
func NewArtifact(id string, docs ...*foundation.Documentation) *Artifact {
	return &Artifact{
		BaseElement: *foundation.NewBaseElement(id, docs...),
	}
}

// *****************************************************************************

// The Group object is an Artifact that provides a visual mechanism to group
// elements of a diagram informally. The grouping is tied to the CategoryValue
// supporting element. That is, a Group is a visual depiction of a single
// CategoryValue. The graphical elements within the Group will be assigned the
// CategoryValue of the Group.
type Group struct {
	foundation.BaseElement

	CategoryValue CategoryValue
}

// NewGroup creates a new Group and returns its pointer
func NewGroup(
	id, categoryValue string,
	docs ...*foundation.Documentation,
) *Group {
	g := Group{
		BaseElement: *foundation.NewBaseElement(id, docs...),
	}

	g.CategoryValue = *NewCategoryValue(g.Id(), categoryValue)

	return &g
}
