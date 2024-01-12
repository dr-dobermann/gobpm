package common

import "github.com/dr-dobermann/gobpm/pkg/model/foundation"

type CallableElement struct {
	foundation.BaseElement

	Name string
}

// NewCallableElement creates a new element and return its pointer.
func NewCallableElement(
	id, name string,
	docs ...*foundation.Documentation,
) *CallableElement {
	return &CallableElement{
		BaseElement: *foundation.NewBaseElement(id, docs...),
		Name:        name,
	}
}
