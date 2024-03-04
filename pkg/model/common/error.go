package common

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// An Error represents the content of an Error Event or the Fault of a failed
// Operation. An ItemDefinition is used to specify the structure of the Error.
// An Error is generated when there is a critical problem in the processing of
// an Activity or when the execution of an Operation failed.
type Error struct {
	foundation.BaseElement

	// The descriptive name of the Error.
	name string

	// For an End Event:
	//   If the result is an Error, then the errorCode MUST be supplied (if
	//   the processType attribute of the Process is set to executable) This
	//   “throws” the Error.
	// For an Intermediate Event within normal flow:
	//   If the trigger is an Error, then the errorCode MUST be entered (if
	//   the processType attribute of the Process is set to executable). This
	//   “throws” the Error.
	// For an Intermediate Event attached to the boundary of an Activity:
	//   If the trigger is an Error, then the errorCode MAY be entered. This
	//   Event “catches” the Error. If there is no errorCode, then any error
	//   SHALL trigger the Event. If there is an errorCode, then only an Error
	//   that matches the errorCode SHALL trigger the Event.
	errorCode string

	// An ItemDefinition is used to define the “payload” of the Error.
	structure *data.ItemDefinition
}

// NewError creates a new error object.
func NewError(name, code string,
	str *data.ItemDefinition,
	baseOpts ...options.Option,
) (*Error, error) {
	name = trim(name)
	if err := checkStr(name, "name should be non-empty"); err != nil {
		return nil, err
	}

	be, err := foundation.NewBaseElement(baseOpts...)
	if err != nil {
		return nil,
			&errs.ApplicationError{
				Err:     err,
				Message: "couldn't build Error",
				Classes: []string{
					errorClass,
					errs.BulidingFailed,
				},
			}
	}

	return &Error{
		BaseElement: *be,
		name:        name,
		errorCode:   code,
		structure:   str,
	}, nil
}

// Name returns Error's name.
func (e *Error) Name() string {
	return e.name
}

// ErrorCode returns error code.
func (e *Error) ErrorCode() string {
	return e.errorCode
}

// Structure returns the copy of Error payload.
func (e *Error) Structure() *data.ItemDefinition {
	str := *e.structure

	return &str
}
