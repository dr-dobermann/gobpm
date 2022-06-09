package vardata

import "github.com/dr-dobermann/gobpm/pkg/variables"

type DataProvider interface {
	AddDataItem(nv DataItem) error
	GetDataItem(vname string) (DataItem, error)
	DelDataItem(vname string) error
	UpdateDataItem(vn string, newVal DataItem) error
}

// DataItem realizes the run-time ItemAwareElement
type DataItem interface {
	Name() string
	Type() variables.Type
	IsCollection() bool

	// returns length of the DataItem if it's a collection
	// if DataItem is not a collection, Len returns 1.
	Len() int

	// if DataItem is a collection, then GetOne fires panic
	// use GetSome instead
	GetOne() variables.Variable

	// Retruns a slice of elements if DataItem is a collection.
	// First element has 0 index.
	// If DataItem is not a collection, then panic fired
	GetSome(from, to int) []variables.Variable

	// Updates value of the DataItem.
	// if it's a collection, the panic fired.
	UpdateOne(newValue *variables.Variable) error

	// Updates single or range elements of collection.
	// If DataItem is not a collection,
	// the errs.ErrIsNotACollection returned.
	UpdateSome(from, to int, newValues []*variables.Variable) error
}
