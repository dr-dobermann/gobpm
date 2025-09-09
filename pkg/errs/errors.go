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
	"fmt"
	"os"
	"strings"
)

const (
	// InvalidObject represents an invalid object error code.
	InvalidObject        = "INVALID_OBJECT"
	// RunFailed represents a run failure error code.
	RunFailed            = "RUN_FAILED"
	// NilDereference represents a nil dereference error code.
	NilDereference       = "NIL_DEREFERENCE"
	// BulidingFailed represents a building failure error code.
	BulidingFailed       = "BUILDING_FAILED"
	// InvalidParameter represents an invalid parameter error code.
	InvalidParameter     = "INVALID_PARAMETER"
	// TypeCastingError represents a type casting error code.
	TypeCastingError     = "INVALID_TYPECASTING"
	// OutOfRangeError represents an out of range error code.
	OutOfRangeError      = "OUT_OF_RANGE"
	// EmptyCollectionError represents an empty collection error code.
	EmptyCollectionError = "COLLECTION_IS_EMPTY"
	// EmptyNotAllowed represents an empty object not allowed error code.
	EmptyNotAllowed      = "EMPTY_OBJ_IS_NOT_ALLOWED"
	// DuplicateObject represents a duplicate object error code.
	DuplicateObject      = "DUPLICATE_OBJECT_ERROR"
	// OperationFailed represents an operation failure error code.
	OperationFailed      = "OPERATION_FAILED"
	// ConditionFailed represents a condition failure error code.
	ConditionFailed      = "CONDITION_FAILED"
	// ObjectNotFound represents an object not found error code.
	ObjectNotFound       = "OBJECT_NOT_FOUND"
	// InvalidState represents an invalid state error code.
	InvalidState         = "INVALID_OBJECT_STATE"
)

// ApplicationError represents a structured application error with classes, message, and details.
type ApplicationError struct {
	Err     error          `json:"error"`
	Message string         `json:"message"`
	Classes []string       `json:"classes"`
	Details map[string]any `json:"details"`
}

// New returns pointer on created with errOptions ApplicationError.
func New(errOpts ...errOption) *ApplicationError {
	eCfg := errConfig{
		err:     nil,
		msg:     defaultMessage,
		classes: []string{},
		details: map[string]any{},
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

// JSON returns the json representation of the ApplicationError ae.
// On failure it panics.
func (ae *ApplicationError) JSON() []byte {
	js, err := json.Marshal(ae)
	if err != nil {
		Panic("couldn't convert application error to json: " + err.Error())
		return nil
	}

	return js
}

// --------------------- error interface ---------------------------------------
func (ae *ApplicationError) Error() string {
	str := ""
	if len(ae.Classes) > 0 {
		str += "Classes: [" + strings.Join(ae.Classes, ", ") + "]\n"
	}

	if ae.Message != "" {
		str += "Message: " + strings.Trim(ae.Message, " ") + "\n"
	}

	if len(ae.Details) > 0 {
		str += "Details:\n"
		for k, v := range ae.Details {
			str += fmt.Sprintf(" %s: %v\n", k, v)
		}
	}

	if ae.Err != nil {
		str += fmt.Errorf("Error: %w", ae.Err).Error() + "\n"
	}

	return str
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
			Err     string         `json:"error"`
			Message string         `json:"message"`
			Classes []string       `json:"classes"`
			Details map[string]any `json:"details"`
		}{
			Err:     errS,
			Message: ae.Message,
			Classes: ae.Classes,
			Details: ae.Details,
		})
}

// -----------------------------------------------------------------------------

// PanicHandler registered for handling panic situation of goBpm.
// If registered handler returns true, then panic is fired according
// to dontPanic settings.
// if return is false, panic ignored as it already handled by
// PanicHandler.
type PanicHandler func(v any) bool

var (
	// flag which prevents panic on unhandled errors.
	// if set to true then error just printed to stderr.
	dontPanic bool

	// panicHandler to handle panic situation.
	panicHook PanicHandler
)

// SetDontPanic sets current behavior of panic.
func SetDontPanic(dp bool) {
	dontPanic = dp
}

// DontPanic return current setup of panic behavior
func DontPanic() bool {
	return dontPanic
}

// Panic write unhandled error into the Stderr or panic dending of the
// dontPanic settings.
func Panic(v any) {
	if panicHook != nil {
		if unhandled := panicHook(v); !unhandled {
			return
		}
	}
	if dontPanic {
		fmt.Fprintln(os.Stderr, v)

		return
	}

	panic(v)
}

// RegisterPanicHandler registers new PanicHandler.
func RegisterPanicHandler(newHandler PanicHandler) error {
	if newHandler == nil {
		return New(
			M("empty handler"),
			C(EmptyNotAllowed))
	}

	panicHook = newHandler

	return nil
}

// DropPanicHandler unregisters panic handler.
func DropPanicHandler() {
	panicHook = nil
}

// HasPanicHandler checks if panicHandler is set.
func HasPanicHandler() bool {
	return panicHook != nil
}
