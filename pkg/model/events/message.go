package events

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/common"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
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
	if med.message.Item() == nil {
		return false
	}

	return med.message.Item().Id() == iDefId
}

// GetItemList returns a list of data.ItemDefinition the EventDefinition
// is based on.
// If EventDefiniton isn't based on any data.ItemDefiniton, empty list
// wil be returned.
func (med *MessageEventDefinition) GetItemsList() []*data.ItemDefinition {
	idd := []*data.ItemDefinition{}

	if med.message.Item() == nil {
		return idd
	}

	return append(idd, med.message.Item())

}

// CloneEvent clones EventDefinition with dedicated data.ItemDefinition
// list.
func (med *MessageEventDefinition) CloneEvent(
	evtData []data.Data,
) (flow.EventDefinition, error) {
	var iDef *data.ItemDefinition

	if len(evtData) != 0 {

		d := evtData[0]

		if d.ItemDefinition().Id() != med.message.Item().Id() {
			return nil,
				errs.New(
					errs.M("message itemDefinition and data itemDefinition have different ids"))
		}

		iDef = d.ItemDefinition()
	}

	msg, err := common.NewMessage(
		med.message.Name(),
		iDef,
		foundation.WithId(med.message.Id()))
	if err != nil {
		return nil,
			errs.New(
				errs.M("couldn't clone Message %q[%s]",
					med.message.Name(), med.message.Id()),
				errs.C(errorClass, errs.BulidingFailed),
				errs.E(err))
	}

	nmed, err := NewMessageEventDefintion(
		msg, med.operation, foundation.WithId(med.Id()))
	if err != nil {
		return nil,
			errs.New(
				errs.M("cloning failed for MessageEventDefinition #%s",
					med.Id()),
				errs.C(errorClass, errs.BulidingFailed),
				errs.E(err))
	}

	return nmed, nil
}

// -----------------------------------------------------------------------------
