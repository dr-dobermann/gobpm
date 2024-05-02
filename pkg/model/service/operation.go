package service

import (
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/common"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/set"
)

// Executor interface runs an Operation and returns its result
type Executor interface {
	// Type returns type of the executore.
	Type() string

	// ErrorClasses returns errors classes listh which may be
	// returned by the Execute call.
	ErrorClasses() []string

	// Execute runs an operation with all parameters provided by
	// Operation entity.
	Execute(op *Operation) (any, error)
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
	//
	// >>>>>  DEVNOTE: original BPMN2 errors functionatity fully covered by
	// gobpm errs package. So errors will consists a list of error classes.
	// Whick
	// errors []*common.Error
	errors *set.Set[string]

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
	executor Executor,
	baseOpts ...options.Option,
) (*Operation, error) {
	name = strings.Trim(name, " ")
	if name == "" {
		return nil,
			errs.New(
				errs.M("operation should have non-empty name"),
				errs.C(errorClass, errs.InvalidParameter))
	}

	be, err := foundation.NewBaseElement(baseOpts...)
	if err != nil {
		return nil, err
	}

	el := []string{}
	if executor != nil {
		el = append(el, executor.ErrorClasses()...)
	}

	return &Operation{
		BaseElement:    *be,
		name:           name,
		inMessage:      inMsg,
		outMessage:     outMsg,
		errors:         set.New[string](el...),
		implementation: executor}, nil
}

// MustOperation creates a new Operation and returns its pointer on succes or
// panics on failure.
func MustOperation(
	name string,
	inMsg, outMsg *common.Message,
	executor Executor,
	baseOpts ...options.Option,
) *Operation {
	o, err := NewOperation(name, inMsg, outMsg, executor)
	if err != nil {
		errs.Panic(err)

		return nil
	}

	return o
}

// Name returns the name of the Operation.
func (o *Operation) Name() string {
	return o.name
}

// IncomingMessage returns incoming Message of the Operation.
func (o *Operation) IncomingMessage() *common.Message {
	return o.inMessage
}

// OutgoingMessage returns outgoing Message of the Operation.
func (o *Operation) OutgoingMessage() *common.Message {
	return o.outMessage
}

// Errors returns a list of Errors which the Operation could return.
func (o *Operation) Errors() []string {
	return o.errors.All()
}

// Implementation returns the Operation implementation.
func (o *Operation) Implementation() Executor {
	return o.implementation
}
