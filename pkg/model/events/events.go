package events

import (
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

const errorClass = "EVENTS_ERRORS"

// trim is a local helper function to trim spaces.
func trim(str string) string {
	return strings.Trim(str, " ")
}

// checkStr local helper function which checks if the str is empty string.
func checkStr(str, msg string) error {
	if str == "" {
		return &errs.ApplicationError{
			Message: msg,
			Classes: []string{
				errorClass,
				errs.InvalidParameter,
			},
		}
	}

	return nil
}

// interfaces check
var (
	_ flow.FlowNode = (*Event)(nil)

	_ flow.SequenceSource = (*StartEvent)(nil)
	_ flow.SequenceTarget = (*EndEvent)(nil)
)
