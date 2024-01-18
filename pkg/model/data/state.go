package data

import "github.com/dr-dobermann/gobpm/pkg/model/foundation"

// Data Object elements can optionally reference a DataState element, which is
// the state of the data contained in the Data Object. The definition of these
// states, e.g., possible values and any specific semantic are out of scope of
// this International Standard. Therefore, BPMN adopters can use the State
// element and the BPMN extensibility capabilities to define their states.
type DataState struct {
	foundation.BaseElement

	Name string
}
