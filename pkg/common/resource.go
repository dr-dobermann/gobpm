package common

import "github.com/dr-dobermann/gobpm/pkg/foundation"

type ResourceParameter struct {
	NamedElement

	Name       string
	Item       ItemDefinition
	IsRequired bool
}

type Resource struct {
	foundation.BaseElement

	Name       string
	Parameters []ResourceParameter
}
