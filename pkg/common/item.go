package common

import (
	"github.com/dr-dobermann/gobpm/pkg/infrastructure"
	"github.com/dr-dobermann/gobpm/pkg/variables"
)

type ItemKind byte

const (
	IkPhysical ItemKind = iota
	IkInformaion
)

type ItemDefinition struct {
	Kind         ItemKind
	StructureRef variables.Variable
	IsCollection bool
	ImportDef    *infrastructure.Import
}
