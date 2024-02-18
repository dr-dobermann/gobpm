package events

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/common"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
)

type MessageEventDefinition struct {
	definition

	// The Message MUST be supplied (if the isExecutable attribute of the
	// Process is set to true).
	message *common.Message

	operation *service.Operation
}

// Type implements Definition interface.
func (*MessageEventDefinition) Type() Trigger {

	return TriggerMessage
}

// NewMessageEventDefinition creates a new MessageEventDefinition and
// returns its pointer. If nil message was given then error returned.
func NewMessageEventDefintion(
	id string,
	msg *common.Message,
	operation *service.Operation,
	docs ...*foundation.Documentation,
) (*MessageEventDefinition, error) {
	if msg == nil {
		return nil,
			&errs.ApplicationError{
				Message: "empty message isn't allowed",
				Classes: []string{
					eventErrorClass,
					errs.InvalidParameter,
				},
			}
	}

	return &MessageEventDefinition{
		definition: *newDefinition(id, docs...),
		message:    msg,
		operation:  operation,
	}, nil
}

// MustMessageEventDefinition returns new MessageEventDefinition. If there is
// error occured, then panic fired.
func MustMessageEventDefinition(
	id string,
	msg *common.Message,
	operation *service.Operation,
	docs ...*foundation.Documentation,
) *MessageEventDefinition {
	med, err := NewMessageEventDefintion(id, msg, operation, docs...)
	if err != nil {
		panic(err.Error())
	}

	return med
}
