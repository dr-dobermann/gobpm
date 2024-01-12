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
	Name string

	// This attribute specifies the input Message of the Operation. An Operation
	// has exactly one input Message.
	InMessage *common.Message

	// This attribute specifies the output Message of the Operation. An Operation
	// has at most one input Message.
	OutMessage *common.Message

	// This attribute specifies errors that the Operation may return. An
	// Operation MAY refer to zero or more Error elements.
	Errors []*common.Error

	//  This attribute allows to reference a concrete artifact in the underlying
	// implementation technology representing that operation, such as a WSDL
	// operation.
	Implementation any
}
