package artifact

import "github.com/dr-dobermann/gobpm/pkg/identity"

type AssociationDirection byte

const (
	AdNone AssociationDirection = iota
	AdOne
	AdBoth
)

type Association struct {
	Artifact

	direction AssociationDirection
	sourceRef identity.Id
	targetRef identity.Id
}
