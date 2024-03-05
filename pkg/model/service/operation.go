package service

import (
	"github.com/dr-dobermann/gobpm/pkg/model/common"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// An Operation defines Messages that are consumed and, optionally, produced
// when the Operation is called. It can also define zero or more errors that
// are returned when operation fails.
type Operation struct {
	foundation.BaseElement

	// The descriptive name of the element.
	name string

	// This attribute specifies the input Message of the Operation. An Operation
	// has exactly one input Message.
	inMessage *common.Message

	// This attribute specifies the output Message of the Operation. An Operation
	// has at most one input Message.
	outMessage *common.Message

	// This attribute specifies errors that the Operation may return. An
	// Operation MAY refer to zero or more Error elements.
	errors []common.Error

	// This attribute allows to reference a concrete artifact in the underlying
	// implementation technology representing that operation, such as a WSDL
	// operation.
	implementation any
}

// Name returns the name of the Operation.
func (o Operation) Name() string {
	return o.name
}

// IncomingMessage returns incoming Message of the Operation.
func (o Operation) IncomingMessage() *common.Message {
	return o.inMessage
}

// OutcomingMessage returns outcoming Message of the Operation.
func (o Operation) OutcomingMessage() *common.Message {
	return o.outMessage
}

// Errors returns a list of Errors which the Operation could return.
func (o Operation) Errors() []common.Error {
	return append([]common.Error{}, o.errors...)
}
