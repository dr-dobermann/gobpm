package model

import "github.com/dr-dobermann/gobpm/pkg/foundation"

type NamedElement struct {
	foundation.BaseElement
	name string
}

func (ne NamedElement) Name() string {
	return ne.name
}

type CallableElement struct {
	NamedElement
	// interfaces []*Interface
	// ioSpec     InputOutputSpecification
	// ioBinds    []InputOutputBinding
}
