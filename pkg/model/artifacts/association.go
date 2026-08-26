package artifacts

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// AssociationDirection defines the direction of an association.
type AssociationDirection string

const (
	// DirectionNone represents no association direction — the standard's
	// default (§8.4.1). The string values are the spec's own literals.
	DirectionNone AssociationDirection = "None"
	// DirectionOne represents one-way association direction.
	DirectionOne AssociationDirection = "One"
	// DirectionBoth represents bi-directional association direction.
	DirectionBoth AssociationDirection = "Both"
)

// validDirections is the standard's closed enumeration (§8.4.1).
var validDirections = map[AssociationDirection]struct{}{
	DirectionNone: {},
	DirectionOne:  {},
	DirectionBoth: {},
}

// An Association links two model elements: typically a TextAnnotation to the
// element it annotates. The compensation shape of BPMN's <association> is NOT
// represented here — the model realizes it as the boundary event's handler
// (ADR-039 §2.4) — so an Association in a container's artifact collection is
// always the plain, non-executable line.
type Association struct {
	source    foundation.Identifyer
	target    foundation.Identifyer
	direction AssociationDirection
	foundation.BaseElement
}

// NewAssociation creates an association from source to target.
//
// Both ends are mandatory — the standard's schema requires sourceRef and
// targetRef (§8.4.1) — so a nil end is refused. An empty direction takes the
// standard's own default None (§8.4.1); anything outside the §8.4.1
// enumeration is refused.
func NewAssociation(
	source, target foundation.Identifyer,
	direction AssociationDirection,
	baseOpts ...options.Option,
) (*Association, error) {
	if source == nil {
		return nil, errs.New(
			errs.M("NewAssociation: a nil source isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if target == nil {
		return nil, errs.New(
			errs.M("NewAssociation: a nil target isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if direction == "" {
		direction = DirectionNone
	}

	if _, ok := validDirections[direction]; !ok {
		return nil, errs.New(
			errs.M("NewAssociation: invalid direction %q (want None, One or Both)",
				string(direction)),
			errs.C(errorClass, errs.InvalidParameter))
	}

	be, err := foundation.NewBaseElement(baseOpts...)
	if err != nil {
		return nil, errs.New(
			errs.M("Association creation failed"),
			errs.C(errorClass, errs.BulidingFailed),
			errs.E(err))
	}

	return &Association{
		source:      source,
		target:      target,
		direction:   direction,
		BaseElement: *be,
	}, nil
}

// MustAssociation tries to create an association and panics on error. For
// tests and examples.
func MustAssociation(
	source, target foundation.Identifyer,
	direction AssociationDirection,
	baseOpts ...options.Option,
) *Association {
	a, err := NewAssociation(source, target, direction, baseOpts...)
	if err != nil {
		errs.Panic(err)
	}

	return a
}

// Source returns the element the association connects from.
func (a *Association) Source() foundation.Identifyer {
	return a.source
}

// Target returns the element the association connects to.
func (a *Association) Target() foundation.Identifyer {
	return a.target
}

// Direction returns the association's direction.
func (a *Association) Direction() AssociationDirection {
	return a.direction
}

// artifact marks Association as one of the package's carried artifact kinds.
func (a *Association) artifact() {}
