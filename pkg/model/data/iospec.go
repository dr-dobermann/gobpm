package data

import "github.com/dr-dobermann/gobpm/pkg/model/foundation"

type DataSet struct {
	Name  string
	Items []*ItemAwareElement
}

type InputOutputSpecification struct {
	foundation.BaseElement
	InputSets  *DataSet
	OutputSets *DataSet
}
