package service

import (
	"context"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/set"
)

// UnspecifiedImplementation represents an unspecified implementation type.
const UnspecifiedImplementation = "##unspecified"

// Implementor interface runs an Operation and returns its result
type Implementor interface {
	// Type returns type of the executor.
	Type() string

	// ErrorClasses returns errors classes list which may be
	// returned by the Execute call.
	//
	// Operation already has ObjectNotFound, EmptyNotAllowed and
	// OperationFailed error classes.
	ErrorClasses() []string

	// Execute runs an operation implementator with in parameter and
	// returns the output result (couldn be nil) and error status.
	Execute(
		ctx context.Context,
		in *data.ItemDefinition,
	) (*data.ItemDefinition, error)
}

// An Operation defines Messages that are consumed and, optionally, produced
// when the Operation is called. It can also define zero or more errors that
// are returned when operation fails.
type Operation struct {
	implementation Implementor
	inMessage      *bpmncommon.Message
	outMessage     *bpmncommon.Message
	errors         *set.Set[string]
	name           string
	foundation.BaseElement
}

// NewOperation creates a new Operation and returns its pointer on success or
// error on failure.
func NewOperation(
	name string,
	inMsg, outMsg *bpmncommon.Message,
	implementor Implementor,
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

	el := []string{
		errs.ObjectNotFound,
		errs.OperationFailed,
		errs.EmptyNotAllowed,
	}

	if implementor != nil {
		el = append(el, implementor.ErrorClasses()...)
	}

	return &Operation{
		BaseElement:    *be,
		name:           name,
		inMessage:      inMsg,
		outMessage:     outMsg,
		errors:         set.New[string](el...),
		implementation: implementor,
	}, nil
}

// MustOperation creates a new Operation and returns its pointer on succes or
// panics on failure.
func MustOperation(
	name string,
	inMsg, outMsg *bpmncommon.Message,
	implementor Implementor,
	baseOpts ...options.Option,
) *Operation {
	o, err := NewOperation(name, inMsg, outMsg, implementor, baseOpts...)
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
func (o *Operation) IncomingMessage() *bpmncommon.Message {
	return o.inMessage
}

// OutgoingMessage returns outgoing Message of the Operation.
func (o *Operation) OutgoingMessage() *bpmncommon.Message {
	return o.outMessage
}

// Errors returns a list of Errors which the Operation could return.
func (o *Operation) Errors() []string {
	return o.errors.All()
}

// Type returns the Operation's implementation type or
// unspecified on empyt implementation.
func (o *Operation) Type() string {
	if o.implementation != nil {
		return o.implementation.Type()
	}

	return UnspecifiedImplementation
}

// Run tries to call implementation.Execute with inMessage as input and
// put it results into outMessage.
func (o *Operation) Run(ctx context.Context) error {
	if o.implementation == nil {
		return errs.New(
			errs.M("no implementation"),
			errs.C(errorClass, errs.ObjectNotFound))
	}

	var in *data.ItemDefinition

	if o.inMessage != nil && o.inMessage.Item() != nil {
		in = o.inMessage.Item()
	}

	out, err := o.implementation.Execute(ctx, in)
	if err != nil {
		return errs.New(
			errs.M("operation %q[%s] execution failed", o.name, o.ID()),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err))
	}

	switch {
	case out != nil && (o.outMessage == nil || o.outMessage.Item() == nil):
		return errs.New(
			errs.M("no output for operation result"),
			errs.C(errorClass, errs.EmptyNotAllowed))

	case out == nil && o.outMessage != nil && o.outMessage.Item() != nil:
		return errs.New(
			errs.M("unexpected empty operation return"),
			errs.C(errorClass, errs.EmptyNotAllowed))

	case out != nil && o.outMessage != nil && o.outMessage.Item() != nil:
		return o.outMessage.Item().Structure().Update(ctx, out.Structure().Get(ctx))
	}

	return nil
}
