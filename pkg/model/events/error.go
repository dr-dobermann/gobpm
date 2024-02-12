package events

import (
	"github.com/dr-dobermann/gobpm/pkg/model/common"
)

type ErrorEventDefinition struct {
	Definition

	// If the trigger is an Error, then an Error payload MAY be provided.
	Error *common.Error
}
