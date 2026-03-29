package bpmncommon

import (
	"strings"

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
	structure *data.ItemDefinition
	name      string
	errorCode string
	foundation.BaseElement
}

// NewError creates a new error object.
func NewError(name, code string,
	str *data.ItemDefinition,
	baseOpts ...options.Option,
) (*Error, error) {
	name = strings.TrimSpace(name)
	if err := errs.CheckStr(name,
		"name should be non-empty", errorClass); err != nil {
		return nil, err
	}

	be, err := foundation.NewBaseElement(baseOpts...)
	if err != nil {
		return nil, err
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
