package artifacts

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

const errorClass = "ARTIFACT_ERROR"

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
func NewArtifact(baseOpts ...foundation.BaseOption) (*Artifact, error) {
	be, err := foundation.NewBaseElement(baseOpts...)
	if err != nil {
		return nil,
			&errs.ApplicationError{
				Err:     err,
				Message: "couldn't create an artifact",
				Classes: []string{
					errorClass,
					errs.BulidingFailed,
				},
			}
	}
	return &Artifact{
		BaseElement: *be,
	}, nil
}

// MustArtifact tries to create a new Artifact and returns its pointer on success.
// If error occured then panic fired.
func MustArtifact(baseOpts ...foundation.BaseOption) *Artifact {
	ar, err := NewArtifact(baseOpts...)
	if err != nil {
		panic(err)
	}

	return ar
}

// *****************************************************************************

// The Group object is an Artifact that provides a visual mechanism to group
// elements of a diagram informally. The grouping is tied to the CategoryValue
// supporting element. That is, a Group is a visual depiction of a single
// CategoryValue. The graphical elements within the Group will be assigned the
// CategoryValue of the Group.
type Group struct {
	foundation.BaseElement

	CategoryValue *CategoryValue
}

// NewGroup creates a new Group and returns its pointer
func NewGroup(
	categoryName string,
	baseOpts ...foundation.BaseOption,
) (*Group, error) {
	be, err := foundation.NewBaseElement(baseOpts...)
	if err != nil {
		return nil,
			&errs.ApplicationError{
				Err:     err,
				Message: "couldn't create group",
				Classes: []string{
					errorClass,
					errs.BulidingFailed,
				},
				Details: map[string]string{
					"category_name": categoryName,
				},
			}
	}

	g := Group{
		BaseElement: *be,
	}

	g.CategoryValue = NewCategoryValue(
		categoryName,
		foundation.WithId(g.Id()))

	return &g, nil
}

// MustGroup tries to create a new Group and returns its pointer on success or
// fires panic on error.
func MustGroup(
	categoryName string,
	baseOpts ...foundation.BaseOption,
) *Group {
	g, err := NewGroup(categoryName, baseOpts...)
	if err != nil {
		panic(err)
	}

	return g
}
