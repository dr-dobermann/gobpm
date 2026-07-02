package activities

import (
	"context"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/exec"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/renv"
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
	operation      service.Operation
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
//   - activities.WithParameters
//   - activities.WithoutParams
//   - foundation.WithId
//   - foundation.WithDoc
func NewServiceTask(
	name string,
	operation service.Operation,
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
func (st *ServiceTask) Clone() (flow.Node, error) {
	t, err := st.clone()
	if err != nil {
		return nil, err
	}

	return &ServiceTask{
		task:           t,
		implementation: st.implementation,
		operation:      st.operation.Clone(),
	}, nil
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

	// The operation is kind-agnostic here: a message operation binds its input
	// from scope and produces its output message; a Go operation reads through
	// the reader and returns its result. re (an renv.RuntimeEnvironment)
	// satisfies the narrow service.DataReader structurally.
	out, err := op.Execute(ctx, re)
	if err != nil {
		return nil,
			errs.New(
				errs.M("operation execution failed"),
				errs.C(errorClass),
				errs.E(err),
				errs.D("service_task_name", st.Name()),
				errs.D("service_task_id", st.ID()),
				errs.D("operation_id", st.operation.ID()))
	}

	if out != nil {
		// Must-constructors: out is non-nil (guarded) and its id is
		// engine-generated and non-empty — a failure here is a programming
		// error, not an input condition.
		res := data.MustParameter(out.ID(),
			data.MustItemAwareElement(out, data.ReadyDataState))

		if err := re.Put(res); err != nil {
			return nil,
				errs.New(
					errs.M("couldn't commit operation result"),
					errs.C(errorClass),
					errs.E(err),
					errs.D("service_task_name", st.Name()),
					errs.D("service_task_id", st.ID()),
					errs.D("operation_id", st.operation.ID()))
		}
	}

	return st.Outgoing(), nil
}

// -----------------------------------------------------------------------------

// interface check
var (
	_ exec.NodeExecutor = (*ServiceTask)(nil)
)
