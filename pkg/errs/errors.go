// errs package consists ApplicationError definition which is
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
	"strings"
)

const (
	InvalidObject        = "INVALID_OBJECT"
	NilDereference       = "NIL_DEREFERENCE"
	BulidingFailed       = "BUILDING_FAILED"
	InvalidParameter     = "INVALID_PARAMETER"
	TypeCastingError     = "INVALID_TYPECASTING"
	OutOfRangeError      = "OUT_OF_RANGE"
	EmptyCollectionError = "COLLECTION_IS_EMPTY"
	//nolint: gosec
	EmptyNotAllowed = "EMPTY_OBJ_IS_NOT_ALLOWED"

	OperationFailed = "OPERATION_FAILED"
)

type ApplicationError struct {
	Err     error
	Message string
	Classes []string
	Details map[string]string
}

func (ap ApplicationError) Error() string {
	str := ""
	if len(ap.Classes) > 0 {
		str += "Classes: [" + strings.Join(ap.Classes, ", ") + "]\n"
	}

	if ap.Message != "" {
		str += "Message: " + strings.Trim(ap.Message, "\n")
	}

	if len(ap.Details) > 0 {
		str += "Details:\n"
		for k, v := range ap.Details {
			str += "  " + k + ": " + v + "\n"
		}
	}

	if ap.Err != nil {
		str += "Error: " + ap.Err.Error() + "\n"
	}

	return str
}
