package service

import (
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/common"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// Executor interface runs an Operation and returns its result
type Executor interface {
	Type() string
	Execute(op *Operation) (any, *common.Error)
}

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

	// This attribute specifies the output Message of the Operation.
	// An Operation has at most one input Message.
	outMessage *common.Message

	// This attribute specifies errors that the Operation may return. An
	// Operation MAY refer to zero or more Error elements.
	errors []*common.Error

	// This attribute allows to reference a concrete artifact in the underlying
	// implementation technology representing that operation, such as a WSDL
	// operation.
	implementation Executor
}

// NewOperation creates a new Operation and returns its pointer on success or
// error on failure.
func NewOperation(
	name string,
	inMsg, outMsg *common.Message,
	errorsList []*common.Error,
	executor Executor,
	baseOpts ...options.Option,
) (*Operation, error) {
	name = strings.Trim(name, " ")
	if name == "" {
		return nil,
			&errs.ApplicationError{
				Message: "empty name isn't allowed for Operation",
				Classes: []string{
					errorClass,
					errs.InvalidParameter},
			}
	}

	be, err := foundation.NewBaseElement(baseOpts...)
	if err != nil {
		return nil,
			&errs.ApplicationError{
				Err:     err,
				Message: "operation building failed",
				Classes: []string{
					errorClass,
					errs.BulidingFailed},
			}
	}

	return &Operation{
		BaseElement:    *be,
		name:           name,
		inMessage:      inMsg,
		outMessage:     outMsg,
		errors:         append([]*common.Error{}, errorsList...),
		implementation: executor}, nil
}

// Name returns the name of the Operation.
func (o Operation) Name() string {
	return o.name
}

// IncomingMessage returns incoming Message of the Operation.
func (o Operation) IncomingMessage() *common.Message {
	return o.inMessage
}

// OutgoingMessage returns outgoing Message of the Operation.
func (o Operation) OutgoingMessage() *common.Message {
	return o.outMessage
}

// Errors returns a list of Errors which the Operation could return.
func (o Operation) Errors() []*common.Error {
	return append([]*common.Error{}, o.errors...)
}

// Implementation returns the Operation implementation.
func (o Operation) Implementation() Executor {
	return o.implementation
}
