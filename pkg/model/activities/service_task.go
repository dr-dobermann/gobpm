package activities

import (
	"context"
	"strings"

	"github.com/dr-dobermann/gobpm/internal/exec"
	"github.com/dr-dobermann/gobpm/internal/renv"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
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
//
// If the Service Task is associated with an Operation, there MUST be a Message
// Data Input on the Service Task and it MUST have an itemDefinition equivalent
// to the one defined by the Message referred to by the inMessageRef attribute
// of the operation. If the operation defines output Messages, there MUST be a
// single Data Output and it MUST have an itemDefinition equivalent to the one
// defined by Message referred to by the outMessageRef attribute of the
// Operation.
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

// NewServiceTask creates a new service task named name and operation as
// service engine with some options.
//
// Available options are:
//   - activities.WithMultyInstance
//   - activities.WithCompensation
//   - activities.WithLoop
//   - activities.WithStartQuantity
//   - activities.WithCompletionQuantity
//   - activities.WithSet
//   - activities.WithoutParams
//   - foundation.WithId
//   - foundation.WithDoc
func NewServiceTask(
	name string,
	operation *service.Operation,
	taskOpts ...options.Option,
) (*ServiceTask, error) {
	name = strings.TrimSpace(name)
	if err := errs.CheckStr(
		name, "empty name isn't allowed for the ServiceTask",
		errorClass,
	); err != nil {
		return nil, err
	}

	if operation == nil {
		return nil,
			errs.New(
				errs.M("operation should be provided for ServiceTask"),
				errs.C(errorClass, errs.EmptyNotAllowed))
	}

	t, err := NewTask(name, taskOpts...)
	if err != nil {
		return nil, err
	}

	return &ServiceTask{
			Task:           *t,
			implementation: operation.Type(),
			operation:      operation,
		},
		nil
}

// Implementation returns the ServiceTask implementation description.
func (st *ServiceTask) Implementation() string {
	return st.implementation
}

// ------------------ flow.Node interface --------------------------------------

// Node returns underlying node object.
func (st *ServiceTask) Node() flow.Node {
	return st
}

// ------------------ flow.Task interface --------------------------------------

// TaskType returns a type of the Task.
func (st *ServiceTask) TaskType() flow.TaskType {
	return flow.ServiceTask
}

// ------------------ exec.NodeExecutor interface ------------------------------

// Exec runs single node and returns its valid
// output sequence flows on success or error on failure.
//
// Exec fills operation input message with data from the scope and
// runs the operation.
// After this it updates tasks output values with output message of the
// operation.
func (st *ServiceTask) Exec(
	ctx context.Context,
	re renv.RuntimeEnvironment,
) ([]*flow.SequenceFlow, error) {
	if re == nil {
		return nil,
			errs.New(
				errs.M("no runtime environment"),
				errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if err := st.loadInputMessage(re); err != nil {
		return nil,
			errs.New(
				errs.M("couldn't set operation's incoming message"),
				errs.C(errorClass),
				errs.E(err),
				errs.D("service_task_name", st.Name()),
				errs.D("service_task_id", st.Id()),
				errs.D("item_id", st.operation.IncomingMessage().Item().Id()),
				errs.D("operation_id", st.operation.Id()),
				errs.D("message_name", st.operation.IncomingMessage().Name()),
				errs.D("message_id", st.operation.IncomingMessage().Id()))
	}

	if err := st.operation.Run(ctx); err != nil {
		return nil,
			errs.New(
				errs.M("operation run failed"),
				errs.E(err),
				errs.D("service_task_name", st.Name()),
				errs.D("service_task_id", st.Id()),
				errs.D("operation_id", st.operation.Id()),
				errs.D("operation_name", st.operation.Name()))
	}

	if err := st.uploadOutputMessage(); err != nil {
		return nil,
			errs.New(
				errs.M("couldn't save operation's outgoing message"),
				errs.C(errorClass),
				errs.E(err),
				errs.D("service_task_name", st.Name()),
				errs.D("service_task_id", st.Id()),
				errs.D("item_id", st.operation.IncomingMessage().Item().Id()),
				errs.D("operation_id", st.operation.Id()),
				errs.D("message_name", st.operation.IncomingMessage().Name()),
				errs.D("message_id", st.operation.IncomingMessage().Id()))
	}

	return st.Outgoing(), nil
}

// loadInputMessage tries to set value of the operation's incoming message
// from scope data if them are Ready..
func (st *ServiceTask) loadInputMessage(re renv.RuntimeEnvironment) error {
	if st.operation.IncomingMessage() == nil ||
		st.operation.IncomingMessage().Item() == nil {
		return nil
	}

	d, err := re.GetDataById(
		st.dataPath,
		st.operation.IncomingMessage().Item().Id())
	if err != nil {
		return errs.New(
			errs.M("couldn't find item definition"),
			errs.E(err))
	}

	if d.State().Name() != data.ReadyDataState.Name() {
		return errs.New(
			errs.M("data state isn't ready"),
		)
	}

	if err := st.operation.IncomingMessage().Item().
		Structure().Update(d.Value().Get()); err != nil {
		return errs.New(
			errs.M("couldn't update operation's incoming message"),
			errs.E(err))
	}

	return nil
}

// uploadOutputMessage uploads operation's output message into task's
// output and set their state to Ready.
func (st *ServiceTask) uploadOutputMessage() error {
	if st.operation.OutgoingMessage() == nil ||
		st.operation.OutgoingMessage().Item() == nil {
		return nil
	}

	outs, err := st.IoSpec.Parameters(data.Output)
	if err != nil {
		return errs.New(
			errs.M("couldn't get task output parameters"))
	}

	for _, o := range outs {
		if o.ItemDefinition().Id() == st.operation.OutgoingMessage().Item().Id() {
			err = st.operation.OutgoingMessage().
				Item().Structure().
				Update(o.ItemDefinition().Structure().Get())
			if err == nil {
				if err := o.UpdateState(data.ReadyDataState); err != nil {
					return errs.New(
						errs.M("couldn't update task's output state to Ready"),
						errs.C(err.Error(), errs.OperationFailed),
						errs.E(err),
						errs.D("task_name", st.Name()),
						errs.D("output_name", o.Name()))
				}
			}

			return err
		}
	}

	return errs.New(
		errs.M("couldn't find task output for operation output message"))
}

// -----------------------------------------------------------------------------

// interface check
var (
	_ exec.NodeExecutor = (*ServiceTask)(nil)
)
