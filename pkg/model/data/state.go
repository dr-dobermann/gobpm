package data

import "github.com/dr-dobermann/gobpm/pkg/model/foundation"

var defaultDataState = DataState{
	BaseElement: *foundation.MustBaseElement(foundation.WithId("UNDEFINED_DATA_STATE")),
	name:        "UNDEFINED",
}

// Data Object elements can optionally reference a DataState element, which is
// the state of the data contained in the Data Object. The definition of these
// states, e.g., possible values and any specific semantic are out of scope of
// this International Standard. Therefore, BPMN adopters can use the State
// element and the BPMN extensibility capabilities to define their states.
type DataState struct {
	foundation.BaseElement

	name string
}

// Name returns the DataState name.
func (ds *DataState) Name() string {
	return ds.name
}
