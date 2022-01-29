package model

import "github.com/dr-dobermann/gobpm/pkg/model/base"

type NamedElement struct {
	base.BaseElement
	name string
}

func (ne NamedElement) Name() string {
	return ne.name
}

type ItemKind uint8

const (
	Information ItemKind = iota
	Physical
)

type CallableElement struct {
	NamedElement
	// interfaces []*Interface
	// ioSpec     InputOutputSpecification
	// ioBinds    []InputOutputBinding
}
