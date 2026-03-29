// Package bpmncommon provides common BPMN model elements and utilities.
//
// This package is part of GoBPM - Business Process Management library for Go.
//
// Author: dr-dobermann (rgabitov@gmail.com)
// GitHub: https://github.com/dr-dobermann/gobpm
// License: See LICENSE file in the project root
package bpmncommon

import (
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// CallableElement represents a callable BPMN element.
type CallableElement struct {
	Name string
	foundation.BaseElement
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
