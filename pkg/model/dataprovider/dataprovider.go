package dataprovider

type DataProvider interface {
	AddDataItem(nv DataItem) error
	GetDataItem(vname string) (DataItem, error)
	DelDataItem(vname string) error
}

// DataItem realizes the run-time ItemAwareElement
type DataItem interface {
	Name() string
	IsCollection() bool

	// returns length of the DataItem if it's a collection
	// if DataItem is not a collection, Len returns 1.
	Len() int

	// GetValue returns a json of underlying data
	GetValue() []byte
}
