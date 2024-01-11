package common

import "github.com/dr-dobermann/gobpm/pkg/model/foundation"

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
	structure *ItemDefinition
}

// NewError creates a new error object.
func NewError(id, name, code string,
	str ItemDefinition,
	docs ...foundation.Documentation,
) Error {
	return Error{
		BaseElement: foundation.NewBaseElement(id, docs...),
		name:        name,
		errorCode:   code,
		structure:   &str,
	}
}

// Name returns Error's name.
func (e Error) Name() string {
	return e.name
}

// ErrorCode returns error code.
func (e Error) ErrorCode() string {
	return e.errorCode
}

// Structure returns the Error payload
func (e Error) Structure() *ItemDefinition {
	str := *e.structure

	return &str
}
