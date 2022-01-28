package gep

import "fmt"

type OperationError struct {
	opName string
	msg    string
	Err    error
}

func (oe OperationError) Error() string {
	return fmt.Sprintf("operation %q caused error %q: %v",
		oe.opName, oe.msg, oe.Err)
}

func NewOpErr(
	opName string,
	err error,
	format string,
	values ...interface{}) OperationError {
	return OperationError{
		opName: opName,
		msg:    fmt.Sprintf(format, values...),
		Err:    err,
	}
}
