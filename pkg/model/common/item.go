package common

import (
	"github.com/dr-dobermann/gobpm/pkg/model/dataprovider"
	"github.com/dr-dobermann/gobpm/pkg/model/infrastructure"
)

type ItemKind byte

const (
	IkPhysical ItemKind = iota
	IkInformaion
)

type ItemDefinition struct {
	Kind ItemKind
	// DataItem already has collection flag in it,
	// so original BPMN IsCollection flag is ommited.
	// Use Item.IsCollection() instead
	Item dataprovider.DataItem

	// DataProvider of the DataItem set in
	// Import structure.
	// Import's dataprovider Source might be empty. That's mean
	// the varible placed in memory out of any storage.
	Import *infrastructure.Import
}
