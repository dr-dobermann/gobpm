package events

import (
	"github.com/dr-dobermann/gobpm/pkg/model/common"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
)

type MessageEventDefinition struct {
	Definition

	// The Message MUST be supplied (if the isExecutable attribute of the
	// Process is set to true).
	Message *common.Message

	Operation *service.Operation
}
