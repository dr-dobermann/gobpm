package activities

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
)

// The Service Task inherits the attributes and model associations of Activity.
// In addition the following constraints are introduced when the Service Task
// references an Operation:
//   - The Service Task has exactly one inputSet and at most one outputSet.
//     It has a single Data Input with an ItemDefinition equivalent to the one
//     defined by the Message referenced by the inMessageRef attribute of the
//     associated Operation.
//     If the Operation defines output Messages, the Service Task has a single
//     Data Output that has an ItemDefinition equivalent to the one defined by
//     the Message referenced by the outMessageRef attribute of the associated
//     Operation.
type ServiceTask struct {
	Task

	// This attribute specifies the technology that will be used to send
	// and receive the Messages. Valid values are "##unspecified" for
	// leaving the implementation technology open, "##WebService" for
	// the Web service technology or a URI identifying any other
	// technology or coordination protocol. A Web service is the default
	// technology.
	implementation string

	// This attribute specifies the operation that is invoked by the
	// Service Task.
	operation *service.Operation
}

func NewServiceTask(
	name string,
	operation *service.Operation,
	taskOpts ...options.Option,
) (*ServiceTask, error) {
	name = trim(name)
	if err := checkStr(
		name, "empty name isn't allowed for the ServiceTask"); err != nil {

		return nil, err
	}

	if operation == nil {
		return nil,
			&errs.ApplicationError{
				Message: "operation should be provided for ServiceTask",
				Classes: []string{
					errorClass,
					errs.BulidingFailed}}
	}

	t, err := NewTask(name, taskOpts...)
	if err != nil {
		return nil,
			&errs.ApplicationError{
				Err:     err,
				Message: "couldn't create a ServiceTask",
				Classes: []string{
					errorClass,
					errs.BulidingFailed}}
	}

	impl := "##unspecified"
	if operation.Implementation() != nil {
		impl = operation.Implementation().Type()
	}

	return &ServiceTask{
			Task:           *t,
			implementation: impl,
			operation:      operation},
		nil
}
