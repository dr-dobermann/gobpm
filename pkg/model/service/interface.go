// Package service provides BPMN service interfaces and implementations.
package service

import (
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// An Interface defines a set of operations that are implemented by Services.
type Interface struct {
	Implementation any
	Name           string
	foundation.BaseElement
	Operations       []Operation
	CallableElements []*bpmncommon.CallableElement
}
