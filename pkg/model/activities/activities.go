// Package activities provides BPMN activity implementations.
package activities

const errorClass = "ACTIVITIES_ERRORS"

// Result-type names a FormalExpression reports via ResultType(), used to guard
// the boolean conditions (loop/flow/completion) and the integer cardinality.
const (
	resultTypeBool = "bool"
	resultTypeInt  = "int"
)
