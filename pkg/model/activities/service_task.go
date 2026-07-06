package activities

import (
	"context"
	"strings"
	"time"

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
	// timeout bounds the in-process operation execution when positive
	// (WithTimeout, SRD-035); non-positive means unbounded.
	timeout time.Duration
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
//   - activities.WithRoles
//   - activities.WithTimeout
//   - foundation.WithID
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

	// Separate the ServiceTask-specific options (e.g. WithTimeout) from the
	// embedded task's options before building the task.
	var sc srvTaskConfig

	baseOpts := make([]options.Option, 0, len(taskOpts))
	for _, o := range taskOpts {
		if sto, ok := o.(SrvTaskOption); ok {
			sto(&sc)

			continue
		}

		baseOpts = append(baseOpts, o)
	}

	t, err := newTask(name, baseOpts...)
	if err != nil {
		return nil, err
	}

	return &ServiceTask{
			task:           *t,
			implementation: operation.Type(),
			operation:      operation,
			timeout:        sc.timeout,
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
		timeout:        st.timeout,
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
	out, err := st.execOperation(ctx, re, op)
	if err != nil {
		return nil, err
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

// execOperation runs op honoring st.timeout. With no timeout (the default) the
// operation runs synchronously on the track goroutine. With a positive timeout
// it runs in a sub-goroutine and execOperation returns as soon as the operation
// finishes, ctx is canceled, or the timeout elapses (SRD-035, ADR-021 §2.9).
// An operation failure is wrapped; a cancellation returns ctx.Err(); a timeout
// returns a self-identifying error that faults the task.
//
// The timeout bounds the TRACK's wait, not the operation: Go cannot terminate a
// goroutine, so an operation that ignores ctx keeps running (and leaks) after a
// timeout — hence the warning. The done channel is buffered so an operation
// that eventually returns still exits cleanly, and the timer uses NewTimer+Stop
// (not time.After) so it is released on every exit path.
func (st *ServiceTask) execOperation(
	ctx context.Context,
	re renv.RuntimeEnvironment,
	op service.Operation,
) (*data.ItemDefinition, error) {
	if st.timeout <= 0 {
		out, err := op.Execute(ctx, re)

		return out, st.wrapOpErr(err)
	}

	type opRes struct {
		out *data.ItemDefinition
		err error
	}

	done := make(chan opRes, 1)
	go func() {
		o, e := op.Execute(ctx, re)
		done <- opRes{out: o, err: e}
	}()

	timer := time.NewTimer(st.timeout)
	defer timer.Stop()

	select {
	case r := <-done:
		return r.out, st.wrapOpErr(r.err)

	case <-ctx.Done():
		return nil, ctx.Err()

	case <-timer.C:
		re.Logger().Warn(
			"service task timed out; its operation goroutine may still be running",
			"task", st.Name(), "timeout", st.timeout)

		return nil,
			errs.New(
				errs.M("service task %q timed out after %s",
					st.Name(), st.timeout),
				errs.C(errorClass, errs.OperationFailed),
				errs.D("service_task_id", st.ID()),
				errs.D("timeout", st.timeout.String()))
	}
}

// wrapOpErr wraps a non-nil operation error with ServiceTask context, or
// returns nil for a nil error.
func (st *ServiceTask) wrapOpErr(err error) error {
	if err == nil {
		return nil
	}

	return errs.New(
		errs.M("operation execution failed"),
		errs.C(errorClass),
		errs.E(err),
		errs.D("service_task_name", st.Name()),
		errs.D("service_task_id", st.ID()),
		errs.D("operation_id", st.operation.ID()))
}

// -----------------------------------------------------------------------------

// interface check
var (
	_ exec.NodeExecutor = (*ServiceTask)(nil)
)
