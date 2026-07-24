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

// NewCallableElement creates a new element and returns its pointer, or an
// error on an invalid option (FIX-026 — caller options are validated, never
// a deferred panic).
func NewCallableElement(
	name string,
	baseOpts ...options.Option,
) (*CallableElement, error) {
	be, err := foundation.NewBaseElement(baseOpts...)
	if err != nil {
		return nil, err
	}

	return &CallableElement{
		BaseElement: *be,
		Name:        name,
	}, nil
}
