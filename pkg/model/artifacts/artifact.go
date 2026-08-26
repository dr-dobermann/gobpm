// Package artifacts provides BPMN artifact implementations.
package artifacts

import (
	"strconv"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

const errorClass = "ARTIFACT_ERRORS"

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

// Artifact is one of the standard's three artifacts a container carries: an
// Association, a TextAnnotation, or a Group (ADR-039 §2.2). Artifacts are
// model-only (SAD-001 §14): parsed and preserved for BPMN loading, never
// executed — no engine decision reads one. The unexported marker keeps the
// set closed to this package's kinds: the engine cannot re-emit an artifact
// kind it does not know, so an open set would admit inert values that die at
// the first export.
type Artifact interface {
	foundation.BaseObject

	artifact()
}

// Append validates arts — no nil entry, no id duplicating existing or each
// other — and returns existing extended with them. It is the one
// implementation of the collection invariant both containers (Process and
// SubProcess) delegate to, so the rule cannot fork between them.
func Append(existing []Artifact, arts ...Artifact) ([]Artifact, error) {
	ids := make(map[string]struct{}, len(existing)+len(arts))
	for _, a := range existing {
		ids[a.ID()] = struct{}{}
	}

	for i, a := range arts {
		if a == nil {
			return nil, errs.New(
				errs.M("artifacts.Append: a nil artifact isn't allowed"),
				errs.C(errorClass, errs.EmptyNotAllowed),
				errs.D("artifact_index", strconv.Itoa(i)))
		}

		if _, ok := ids[a.ID()]; ok {
			return nil, errs.New(
				errs.M("artifacts.Append: duplicate artifact id %q", a.ID()),
				errs.C(errorClass, errs.DuplicateObject),
				errs.D("artifact_index", strconv.Itoa(i)))
		}

		ids[a.ID()] = struct{}{}
	}

	return append(existing, arts...), nil
}

// *****************************************************************************

// The Group object is an Artifact that provides a visual mechanism to group
// elements of a diagram informally. The grouping is tied to the CategoryValue
// supporting element. That is, a Group is a visual depiction of a single
// CategoryValue. The graphical elements within the Group will be assigned the
// CategoryValue of the Group.
type Group struct {
	CategoryValue *CategoryValue
	foundation.BaseElement
}

// NewGroup creates a new Group and returns its pointer
func NewGroup(
	categoryName string,
	baseOpts ...options.Option,
) (*Group, error) {
	be, err := foundation.NewBaseElement(baseOpts...)
	if err != nil {
		return nil, err
	}

	g := Group{
		BaseElement: *be,
	}

	cv, err := NewCategoryValue(categoryName, foundation.WithID(g.ID()))
	if err != nil {
		return nil, err
	}

	g.CategoryValue = cv

	return &g, nil
}

// MustGroup tries to create a new Group and returns its pointer on success or
// fires panic on error.
func MustGroup(
	categoryName string,
	baseOpts ...options.Option,
) *Group {
	g, err := NewGroup(categoryName, baseOpts...)
	if err != nil {
		errs.Panic(err)
	}

	return g
}

// artifact marks Group as one of the package's carried artifact kinds.
func (g *Group) artifact() {}
