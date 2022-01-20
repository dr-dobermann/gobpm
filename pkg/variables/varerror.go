package variables

import "fmt"

type VariableError struct {
	vName string
	vType Type
	msg   string
	Err   error
}

func (ve VariableError) Error() string {
	return fmt.Sprintf("variable '%s'(%s) error %q: %v",
		ve.vName, ve.vType.String(), ve.msg, ve.Err)
}

type VStoreError struct {
	msg string
	Err error
}

func (vse VStoreError) Error() string {
	return fmt.Sprintf("varStore error %q: %v",
		vse.msg, vse.Err)
}
