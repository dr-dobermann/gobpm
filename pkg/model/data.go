package model

import (
	"github.com/dr-dobermann/gobpm/pkg/base"
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

type DataAccessor interface {
	Name() string
	IsCollection() bool

	// returns length of the DataAccessor if it's a collection
	// if DataAccessor is not a collection, Len returns 1.
	Len() int

	// if DataAccessor is a collection, then GetOne fires panic
	// use GetSome instead
	GetOne() vars.Variable

	// Retruns a slice of elements if DataAccessor is a collection.
	// First element has 0 index.
	// If DataAccessor is not a collection, then panic fired
	GetSome(from, to int) []vars.Variable

	// Updates value of the DataAccessor.
	// if it's a collection, the panic fired.
	UpdateOne(newValue *vars.Variable) error

	// Updates single or range elements of collection.
	// If DataAccessor is not a collection,
	// the errs.ErrIsNotACollection returned.
	UpdateSome(from, to int, newValues []*vars.Variable) error
}

// ItemDefinition defines an Item to store a single value or
// a collection of values
type ItemDefinition struct {
	ItemType     vars.Type
	IsCollection bool
	Accessor     DataAccessor
}

// ItemAwareElement creates a link to a single value or a
// collection of the values
type ItemAwareElement struct {
	base.BaseElement
	Name string
	IDef ItemDefinition
}

type DataSet struct {
	Name  string
	Items []*ItemAwareElement
}

type InputOutputSpecification struct {
	base.BaseElement
	InputSets  []*DataSet
	OutputSets []*DataSet
}
