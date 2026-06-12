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

// ServiceTask inherits the attributes and model associations of Activity.
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
	operation      *service.Operation
	implementation string
	task
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

	t, err := newTask(name, taskOpts...)
	if err != nil {
		return nil, err
	}

	return &ServiceTask{
			task:           *t,
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

// Clone returns a per-instance copy of the ServiceTask. The embedded task is
// cloned (config shared by reference, fresh activity shell, zero dataPath) and
// the implementation string is copied. The operation gets a per-instance clone
// (shared definition, fresh message carriers) so the exec-mutated message item
// state is not shared across concurrent instances.
func (st *ServiceTask) Clone() flow.Node {
	return &ServiceTask{
		task:           st.clone(),
		implementation: st.implementation,
		operation:      st.operation.Clone(),
	}
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
// Exec runs the operation on a PER-EXECUTION clone (its message carriers are
// exec-mutated state — ADR-010 §2.3): the input message is filled from the
// execution's data resolution, the operation runs, and its result is handed
// to the frame as node-produced data, which the producer stage copies into
// the execution's output instance.
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

	op := st.operation.Clone()

	err := st.loadInputMessage(ctx, re, op)
	if err == nil {
		err = op.Run(ctx)
		if err == nil {
			err = st.uploadOutputMessage(re, op)
			if err == nil {
				return st.Outgoing(), nil
			}
		}
	}

	return nil,
		errs.New(
			errs.M("operation execution failed"),
			errs.C(errorClass),
			errs.E(err),
			errs.D("service_task_name", st.Name()),
			errs.D("service_task_id", st.ID()),
			errs.D("operation_id", st.operation.ID()))
}

// loadInputMessage fills the per-execution operation's incoming message from
// the execution's data resolution (frame input instances first).
func (st *ServiceTask) loadInputMessage(
	ctx context.Context,
	re renv.RuntimeEnvironment,
	op *service.Operation,
) error {
	if op.IncomingMessage() == nil ||
		op.IncomingMessage().Item() == nil {
		return nil
	}

	d, err := re.GetDataByID(op.IncomingMessage().Item().ID())
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

	if err := op.IncomingMessage().Item().
		Structure().Update(ctx, d.Value().Get(ctx)); err != nil {
		return errs.New(
			errs.M("couldn't update operation's incoming message"),
			errs.E(err))
	}

	return nil
}

// uploadOutputMessage hands the operation's result to the execution frame as
// node-produced data; the producer stage (updateOutputs) copies it into the
// not-Ready output instance whose ItemDefinition matches the outgoing
// message item.
func (st *ServiceTask) uploadOutputMessage(
	re renv.RuntimeEnvironment,
	op *service.Operation,
) error {
	if op.OutgoingMessage() == nil ||
		op.OutgoingMessage().Item() == nil {
		return nil
	}

	item := op.OutgoingMessage().Item()

	// Must-constructors: the item is non-nil (guarded above) and its id is
	// engine-generated and non-empty — a failure here is a programming
	// error, not an input condition.
	res := data.MustParameter(item.ID(),
		data.MustItemAwareElement(item, data.ReadyDataState))

	return re.Put(res)
}

// -----------------------------------------------------------------------------

// interface check
var (
	_ exec.NodeExecutor = (*ServiceTask)(nil)
)
