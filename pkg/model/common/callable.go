package common

import (
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// Callable
type CallableElement struct {
	foundation.BaseElement

	Name string
}

// NewCallableElement creates a new element and return its pointer.
func NewCallableElement(
	name string,
	baseOpts ...options.Option,
) *CallableElement {
	return &CallableElement{
		BaseElement: *foundation.MustBaseElement(baseOpts...),
		Name:        name,
	}
}
