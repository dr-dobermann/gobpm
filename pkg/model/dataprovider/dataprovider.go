package dataprovider

type DataProvider interface {
	AddDataItem(item DataItem) error
	GetDataItem(name string) (DataItem, error)
	DelDataItem(name string) error
}

// DataItem realizes the run-time ItemAwareElement
type DataItem interface {
	IsCollection() bool

	// returns length of the DataItem if it's a collection
	// if DataItem is not a collection, Len returns 1.
	Len() int

	// Copy creates and returns an DataItem's copy.
	Copy() DataItem

	// GetValue returns a map of underlying DataItem data.
	// This method is used on json marshalling of Message content.
	GetValue() map[string]interface{}

	// UpdateValue gets new value nv and trys to update
	// its current state with a nv.
	// It it's imposible error returned.
	// This method is used on json unmarshalling of Message content.
	UpdateValue(nv map[string]interface{}) error

	GetGuts() interface{}
}

// func Retrieve[T any](di DataItem) (*T, error) {

// 	guts := di.GetGuts()
// 	v, ok := guts.(T)
// 	if !ok {
// 		return nil, fmt.Errorf("Retrieval assertion error")
// 	}

// 	return &v, nil
// }
