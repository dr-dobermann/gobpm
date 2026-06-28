package events

import (
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
)

// BpmnError is the typed error an activity raises to signal a modeled BPMN Error
// (BPMN §10.5.6). Its Code is the Error.errorCode the engine matches against an
// Error boundary's errorRef: a failing activity whose BpmnError.Code equals a
// boundary's errorCode routes to that boundary's exception flow instead of
// faulting the instance (SRD-029 FR-9). A plain (untyped) error keeps the
// instance-fault behavior. Err carries the optional underlying cause.
type BpmnError struct {
	Err  error
	Code string
}

// NewBpmnError builds a BpmnError with the given errorCode and optional cause.
// An empty code is rejected (a BPMN Error is identified by its code, and an
// uncoded error cannot match any boundary), per the validate-all-public-params
// rule. The error is self-identifying through its Code.
func NewBpmnError(code string, cause error) (*BpmnError, error) {
	if strings.TrimSpace(code) == "" {
		return nil,
			errs.New(
				errs.M("NewBpmnError: an empty error code isn't allowed"),
				errs.C(errorClass, errs.EmptyNotAllowed))
	}

	return &BpmnError{Code: code, Err: cause}, nil
}

// Error implements the error interface, self-identifying by code.
func (e *BpmnError) Error() string {
	if e.Err != nil {
		return "bpmn error [" + e.Code + "]: " + e.Err.Error()
	}

	return "bpmn error [" + e.Code + "]"
}

// Unwrap exposes the underlying cause for errors.Is/As chains.
func (e *BpmnError) Unwrap() error {
	return e.Err
}
