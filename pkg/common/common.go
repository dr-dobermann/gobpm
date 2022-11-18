package common

import (
	"github.com/dr-dobermann/gobpm/pkg/foundation"
	"github.com/dr-dobermann/gobpm/pkg/identity"
)

type NamedElement struct {
	foundation.BaseElement

	name string
}

func (ne *NamedElement) Name() string {
	return ne.name
}

func (ne *NamedElement) SetName(name string) {
	ne.name = name
}

type CallableElement struct {
	NamedElement
	// interfaces []*Interface
	// ioSpec     InputOutputSpecification
	// ioBinds    []InputOutputBinding
}

func NewNamedElement(id identity.Id, name string) *NamedElement {
	return &NamedElement{BaseElement: *foundation.New(id),
		name: name}
}
