package events

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/common"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
)

type MessageEventDefinition struct {
	definition

	// The Message MUST be supplied (if the isExecutable attribute of the
	// Process is set to true).
	message *common.Message

	operation *service.Operation
}

// NewMessageEventDefinition creates a new MessageEventDefinition and
// returns its pointer. If nil message was given then error returned.
func NewMessageEventDefintion(
	msg *common.Message,
	operation *service.Operation,
	baseOpts ...options.Option,
) (*MessageEventDefinition, error) {
	if msg == nil {
		return nil,
			errs.New(
				errs.M("empty message isn't allowed"),
				errs.C(errorClass, errs.InvalidParameter))
	}

	d, err := newDefinition(baseOpts...)
	if err != nil {
		return nil, err
	}

	return &MessageEventDefinition{
		definition: *d,
		message:    msg,
		operation:  operation,
	}, nil
}

// MustMessageEventDefinition returns new MessageEventDefinition. If there is
// error occured, then panic fired.
func MustMessageEventDefinition(
	msg *common.Message,
	operation *service.Operation,
	baseOpts ...options.Option,
) *MessageEventDefinition {
	med, err := NewMessageEventDefintion(msg, operation, baseOpts...)
	if err != nil {
		errs.Panic(err)
	}

	return med
}

// Message returns a message of the MessageEventDefinition.
func (med *MessageEventDefinition) Message() *common.Message {
	return med.message
}

// Operation returns an operation of the MessageEventDefinition.
func (med *MessageEventDefinition) Operation() *service.Operation {
	return med.operation
}

// ---------------- flow.EventDefinition interface -----------------------------

// Type returns the MessageEventDefition's flow.EventTrigger.
func (*MessageEventDefinition) Type() flow.EventTrigger {

	return flow.TriggerMessage
}

// CheckItemDefinition check if definition is related with
// data.ItemDefinition with iDefId Id.
func (med *MessageEventDefinition) CheckItemDefinition(iDefId string) bool {

	return med.message.Item().Id() == iDefId
}

// -----------------------------------------------------------------------------
