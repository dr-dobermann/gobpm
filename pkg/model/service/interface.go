// Package service provides BPMN service interfaces and implementations.
package service

import (
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// An Interface defines a set of operations that are implemented by Services.
type Interface struct {
	foundation.BaseElement

	// The descriptive name of the element.
	Name string

	// This attribute specifies operations that are defined as part of the
	// Interface. An Interface has at least one Operation.
	Operations []Operation

	// The CallableElements that use this Interface.
	CallableElements []*bpmncommon.CallableElement

	// This attribute allows to reference a concrete artifact in the underlying
	// implementation technology representing that interface, such as a WSDL
	// porttype.
	Implementation any
}
