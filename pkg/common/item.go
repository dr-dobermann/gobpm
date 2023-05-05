package common

import (
	"github.com/dr-dobermann/gobpm/pkg/dataprovider"
	"github.com/dr-dobermann/gobpm/pkg/infrastructure"
)

type ItemKind byte

const (
	IkPhysical ItemKind = iota
	IkInformaion
)

type ItemDefinition struct {
	Kind ItemKind
	// DataItem already has collection flag in it,
	// so original BPMN IsCollection flag is ommited
	Item dataprovider.DataItem

	// DataProvider of the DataItem set in
	// Import structure
	Import *infrastructure.Import
}
