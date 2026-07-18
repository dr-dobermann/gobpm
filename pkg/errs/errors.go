// Package errs provides ApplicationError definition which is
// used as a standard error in the gobpm library.
//
//	    type ApplicationError struct {
//	        Err     error
//		    Message string
//	        Classes []string
//	        Details map[string]string
//	    }
//
// ApplicationError implements Error interface and could be used whenever
// error is expecting as a result.
//
// errs provides some standard error classes plus every module could have
// itsown errorClass as package variable to indicate error source.
//
// not all fields of ApplicationError aren't demaded by default.
// Only Message and Classes should be filled to present enough information
// about an error.
package errs

import (
	"encoding/json"
	"errors"
	"strings"
)

const (
	// InvalidObject represents an invalid object error code.
	InvalidObject = "INVALID_OBJECT"
	// RunFailed represents a run failure error code.
	RunFailed = "RUN_FAILED"
	// NilDereference represents a nil dereference error code.
	NilDereference = "NIL_DEREFERENCE"
	// BulidingFailed represents a building failure error code.
	BulidingFailed = "BUILDING_FAILED"
	// InvalidParameter represents an invalid parameter error code.
	InvalidParameter = "INVALID_PARAMETER"
	// TypeCastingError represents a type casting error code.
	TypeCastingError = "INVALID_TYPECASTING"
	// OutOfRangeError represents an out of range error code.
	OutOfRangeError = "OUT_OF_RANGE"
	// EmptyCollectionError represents an empty collection error code.
	EmptyCollectionError = "COLLECTION_IS_EMPTY"
	// EmptyNotAllowed represents an empty object not allowed error code.
	EmptyNotAllowed = "EMPTY_OBJ_IS_NOT_ALLOWED"
	// DuplicateObject represents a duplicate object error code.
	DuplicateObject = "DUPLICATE_OBJECT_ERROR"
	// OperationFailed represents an operation failure error code.
	OperationFailed = "OPERATION_FAILED"
	// ConditionFailed represents a condition failure error code.
	ConditionFailed = "CONDITION_FAILED"
	// ObjectNotFound represents an object not found error code.
	ObjectNotFound = "OBJECT_NOT_FOUND"
	// InvalidState represents an invalid state error code.
	InvalidState = "INVALID_OBJECT_STATE"
)

// ApplicationError represents a structured application error with classes, message, and details.
type ApplicationError struct {
	Err error `json:"error"`
	// Details are pre-stringified diagnostic key/values. Keeping them string
	// (not any) makes error construction allocation-lean and Error()/JSON()
	// reflection-free — and makes JSON() infallible (FIX-019).
	Details map[string]string `json:"details"`
	Message string            `json:"message"`
	Classes []string          `json:"classes"`
}

// New returns pointer on created with errOptions ApplicationError.
func New(errOpts ...errOption) *ApplicationError {
	eCfg := errConfig{
		err:     nil,
		msg:     defaultMessage,
		classes: []string{},
		details: map[string]string{},
	}

	ee := make([]error, 0, len(errOpts)+1)
	for _, o := range errOpts {
		if o != nil {
			if err := o.apply(&eCfg); err != nil {
				ee = append(ee, err)
			}
		}
	}

	if len(ee) > 0 {
		eCfg.err = errors.Join(append(ee, eCfg.err)...)
	}

	return eCfg.newError()
}

// HasClass checks if the ApplicationError has class errorClass.
func (ae *ApplicationError) HasClass(class string) bool {
	for _, c := range ae.Classes {
		if c == class {
			return true
		}
	}

	return false
}

// JSON returns the JSON representation of the ApplicationError ae. It neither
// panics nor swallows the marshal error — it propagates it, as a library
// should. With Details map[string]string and scalar/slice-of-scalar fields the
// error is unreachable in practice (FIX-019), but surfacing it keeps the
// contract honest rather than hiding a theoretical failure.
func (ae *ApplicationError) JSON() ([]byte, error) {
	return json.Marshal(ae)
}

// Error returns a string representation of the ApplicationError. It builds the
// text with a strings.Builder and plain string writes — no fmt/reflection —
// since every part (classes, message, details, wrapped error) is already a
// string (FIX-019).
func (ae *ApplicationError) Error() string {
	var b strings.Builder

	if len(ae.Classes) > 0 {
		b.WriteString("Classes: [")
		b.WriteString(strings.Join(ae.Classes, ", "))
		b.WriteString("]\n")
	}

	if ae.Message != "" {
		b.WriteString("Message: ")
		b.WriteString(strings.Trim(ae.Message, " "))
		b.WriteString("\n")
	}

	if len(ae.Details) > 0 {
		b.WriteString("Details:\n")
		for k, v := range ae.Details {
			b.WriteString(" ")
			b.WriteString(k)
			b.WriteString(": ")
			b.WriteString(v)
			b.WriteString("\n")
		}
	}

	if ae.Err != nil {
		b.WriteString("Error: ")
		b.WriteString(ae.Err.Error())
		b.WriteString("\n")
	}

	return b.String()
}

func (ae *ApplicationError) Unwrap() error {
	if ae.Err != nil {
		return ae.Err
	}

	return nil
}

// MarshalJSON implements the json.Marshaler interface for ApplicationError.
func (ae ApplicationError) MarshalJSON() ([]byte, error) {
	errS := "<nil>"
	if ae.Err != nil {
		errS = ae.Err.Error()
	}

	return json.Marshal(
		struct {
			Details map[string]string `json:"details"`
			Err     string            `json:"error"`
			Message string            `json:"message"`
			Classes []string          `json:"classes"`
		}{
			Err:     errS,
			Message: ae.Message,
			Classes: ae.Classes,
			Details: ae.Details,
		})
}

// -----------------------------------------------------------------------------

// Panic raises v as a runtime panic — the single chokepoint the library's
// Must* constructors and invariant guards route through (~33 call sites). It is
// a thin, uniform indirection over the builtin, kept so those sites read the
// same way and so a future diagnostic hook has one place to attach. It carries
// no configurable behavior: a host that must not crash on a library panic
// recovers at its own boundary.
func Panic(v any) {
	panic(v)
}
