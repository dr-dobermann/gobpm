package common

import "github.com/dr-dobermann/gobpm/pkg/model/foundation"

type CallableElement struct {
	foundation.BaseElement

	Name string
}

// NewCallableElement creates a new element and return its pointer.
func NewCallableElement(
	name string,
	baseOpts ...foundation.BaseOption,
) *CallableElement {
	return &CallableElement{
		BaseElement: *foundation.MustBaseElement(baseOpts...),
		Name:        name,
	}
}
