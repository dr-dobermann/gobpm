package data

import "github.com/dr-dobermann/gobpm/pkg/model/foundation"

type DataInput struct {
	Name string

	ItemAwareElement
}

type DataOutput struct {
	Name string

	ItemAwareElement
}

type InputOutputSpecification struct {
	foundation.BaseElement

	DataIntputs []*DataInput
	DataOutputs []*DataOutput
}
