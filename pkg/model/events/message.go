package events

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
)

// MessageEventDefinition represents a message event definition.
type MessageEventDefinition struct {
	message   *bpmncommon.Message
	operation service.Operation
	definition
}

// Compile-time conformance (FIX-011): CloneEventDefinition must match
// flow.EventDefCloner, else the throw-path clone-with-data step silently no-ops.
var (
	_ flow.EventDefinition = (*MessageEventDefinition)(nil)
	_ flow.EventDefCloner  = (*MessageEventDefinition)(nil)
)

// NewMessageEventDefinition creates a new MessageEventDefinition and
// returns its pointer. If nil message was given then error returned.
func NewMessageEventDefinition(
	msg *bpmncommon.Message,
	operation service.Operation,
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
// error occurred, then panic fired.
func MustMessageEventDefinition(
	msg *bpmncommon.Message,
	operation service.Operation,
	baseOpts ...options.Option,
) *MessageEventDefinition {
	med, err := NewMessageEventDefinition(msg, operation, baseOpts...)
	if err != nil {
		errs.Panic(err)
	}

	return med
}

// Message returns a message of the MessageEventDefinition.
func (med *MessageEventDefinition) Message() *bpmncommon.Message {
	return med.message
}

// Operation returns an operation of the MessageEventDefinition.
func (med *MessageEventDefinition) Operation() service.Operation {
	return med.operation
}

// ---------------- flow.EventDefinition interface -----------------------------

// Type returns the MessageEventDefition's flow.EventTrigger.
func (*MessageEventDefinition) Type() flow.EventTrigger {
	return flow.TriggerMessage
}

// CheckItemDefinition check if definition is related with
// data.ItemDefinition with iDefID Id.
func (med *MessageEventDefinition) CheckItemDefinition(iDefID string) bool {
	if med.message.Item() == nil {
		return false
	}

	return med.message.Item().ID() == iDefID
}

// GetItemsList returns a list of data.ItemDefinition the EventDefinition
// is based on.
// If EventDefinition isn't based on any data.ItemDefinition, empty list
// will be returned.
func (med *MessageEventDefinition) GetItemsList() []*data.ItemDefinition {
	idd := make([]*data.ItemDefinition, 0, 1)

	if med.message.Item() == nil {
		return idd
	}

	return append(idd, med.message.Item())
}

// CloneEventDefinition clones EventDefinition with dedicated data.ItemDefinition
// list. It satisfies flow.EventDefCloner (the method was previously named
// CloneEvent and so never satisfied the interface — FIX-011).
func (med *MessageEventDefinition) CloneEventDefinition(
	evtData []data.Data,
) (flow.EventDefinition, error) {
	var iDef *data.ItemDefinition

	if len(evtData) != 0 {
		d := evtData[0]

		if d.ItemDefinition().ID() != med.message.Item().ID() {
			return nil,
				errs.New(
					errs.M("message itemDefinition and data itemDefinition have different ids"))
		}

		iDef = d.ItemDefinition()
	}

	msg, err := bpmncommon.NewMessage(
		med.message.Name(),
		iDef,
		foundation.WithID(med.message.ID()))
	if err != nil {
		return nil,
			errs.New(
				errs.M("couldn't clone Message %q[%s]",
					med.message.Name(), med.message.ID()),
				errs.C(errorClass, errs.BulidingFailed),
				errs.E(err))
	}

	nmed, err := NewMessageEventDefinition(
		msg, med.operation, foundation.WithID(med.ID()))
	if err != nil {
		return nil,
			errs.New(
				errs.M("cloning failed for MessageEventDefinition #%s",
					med.ID()),
				errs.C(errorClass, errs.BulidingFailed),
				errs.E(err))
	}

	return nmed, nil
}

// CloneForInstance returns a per-instance copy of the MessageEventDefinition
// with a FRESH id, sharing the (immutable) message and operation by reference.
// Node cloning (Event.clone) uses it so each process instance's message receiver
// registers a DISTINCT EventHub waiter (keyed by eDef id): without it concurrent
// instances waiting on the same message would share one waiter and a single
// point-to-point message would wake them all (SRD-017 §4.3). It is the opposite
// of CloneEventDefinition, which keeps the id so a FIRED event still matches its waiter.
func (med *MessageEventDefinition) CloneForInstance() flow.EventDefinition {
	return &MessageEventDefinition{
		definition: definition{BaseElement: *foundation.MustBaseElement()},
		message:    med.message,
		operation:  med.operation,
	}
}

// -----------------------------------------------------------------------------
