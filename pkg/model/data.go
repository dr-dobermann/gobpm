package model

import (
	"github.com/dr-dobermann/gobpm/pkg/foundation"
	vars "github.com/dr-dobermann/gobpm/pkg/variables"
)

// type Import struct {
// 	impType   string
// 	location  string
// 	namespace string
// }

// type Error struct {
// 	NamedElement
// 	errorCode string
// }

type DataStorageType uint8

const (
	Embedded DataStorageType = iota
	DataBase
)

// ItemDefinition defines an Item to store a single value or
// a collection of values
type ItemDefinition struct {
	ItemType       vars.Type
	IsCollection   bool
	StorageType    DataStorageType
	StorageDetails string
}

// ItemAwareElement creates a link to a single value or a
// collection of the values
type ItemAwareElement struct {
	foundation.BaseElement
	Name string
	IDef ItemDefinition 
}

type DataSet struct {
	Name  string
	Items []*ItemAwareElement
}

type InputOutputSpecification struct {
	foundation.BaseElement
	InputSets  []*DataSet
	OutputSets []*DataSet
}
