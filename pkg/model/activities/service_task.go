package activities

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/eventproc"
	"github.com/dr-dobermann/gobpm/pkg/exec"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/dr-dobermann/gobpm/pkg/renv"
	"github.com/dr-dobermann/gobpm/pkg/tasks"
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
	operation   service.Operation
	errorMapper tasks.ErrorMapper
	retryPolicy tasks.RetryPolicy
	// outputMapping shapes a worker's raw Complete body into the output
	// (WithOutputMapping, SRD-037 FR-7); empty = direct reconciliation.
	outputMapping []tasks.OutputRule
	// outcome stashes the worker's report (set by ProcessEvent, read by Exec on
	// resume — same goroutine). Runtime-only; nil on a fresh Clone (SRD-037 §3.5).
	outcome        *tasks.WorkerOutcome
	implementation string
	workerTopic    tasks.Topic
	// statusVar / statusOverwrite are the WithStatus config: the task-scoped
	// variable a Business Status writes, and whether it may overwrite (SRD-037 FR-5).
	statusVar string
	task
	timeout time.Duration
	// trustMode selects the worker-outcome policy locus (WorkerTrusted /
	// EngineAuthoritative); trustUnset resolves to the engine default at enqueue
	// (SRD-039 M9).
	trustMode       tasks.TrustMode
	statusOverwrite bool
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
//   - activities.WithWorker
//   - activities.WithErrorMapper
//   - activities.WithStatus
//   - activities.WithOutputMapping
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
			if err := sto(&sc); err != nil {
				return nil, err
			}

			continue
		}

		baseOpts = append(baseOpts, o)
	}

	// WithWorker is valid only on a message operation: a Go operation is an
	// in-process closure with no shippable message boundary (SRD-036 §2.3).
	if sc.workerTopic != "" && operation.Type() == gooper.GoOperType {
		return nil,
			errs.New(
				errs.M("WithWorker requires a message-operation ServiceTask; "+
					"%q has a Go operation", name),
				errs.C(errorClass, errs.InvalidParameter),
				errs.D("worker_topic", string(sc.workerTopic)))
	}

	// WithErrorMapper / WithStatus / WithOutputMapping govern the worker outcome —
	// meaningless on an in-process ServiceTask, so require WithWorker (SRD-037 §3.4).
	if sc.workerTopic == "" &&
		(sc.errorMapper != nil || sc.retryPolicy != nil || sc.trustSet ||
			sc.statusVar != "" || len(sc.outputMapping) > 0) {
		return nil,
			errs.New(
				errs.M("WithErrorMapper/WithRetryPolicy/WithWorkerTrust/WithStatus/"+
					"WithOutputMapping require a worker-dispatched ServiceTask "+
					"(WithWorker); %q has none", name),
				errs.C(errorClass, errs.InvalidParameter))
	}

	t, err := newTask(name, baseOpts...)
	if err != nil {
		return nil, err
	}

	return &ServiceTask{
			task:            *t,
			implementation:  operation.Type(),
			operation:       operation,
			timeout:         sc.timeout,
			workerTopic:     sc.workerTopic,
			errorMapper:     sc.errorMapper,
			retryPolicy:     sc.retryPolicy,
			trustMode:       sc.trustMode,
			outputMapping:   sc.outputMapping,
			statusVar:       sc.statusVar,
			statusOverwrite: sc.statusOverwrite,
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
		task:            t,
		implementation:  st.implementation,
		operation:       st.operation.Clone(),
		timeout:         st.timeout,
		workerTopic:     st.workerTopic,
		errorMapper:     st.errorMapper,
		retryPolicy:     st.retryPolicy,
		trustMode:       st.trustMode,
		outputMapping:   st.outputMapping,
		statusVar:       st.statusVar,
		statusOverwrite: st.statusOverwrite,
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

	// A worker-dispatched ServiceTask runs Exec only on RESUME — checkNodeType
	// parks it (binding input + enqueuing a job) before the in-process path is
	// ever reached, so here we classify + apply the worker's outcome (SRD-037).
	if st.workerTopic != "" {
		return st.execWorkerOutcome(ctx, re)
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

	return st.selectOutgoing(ctx, re)
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
		// Log-and-return of the same failure is forbidden (ADR-022 v.1 §2.1);
		// the timeout is reported once, by the returned error at its fault
		// boundary. The Warn's unique nuance (the operation goroutine may still
		// be running) folds into the message and details.
		return nil,
			errs.New(
				errs.M("service task %q timed out after %s; its operation "+
					"goroutine may still be running", st.Name(), st.timeout),
				errs.C(errorClass, errs.OperationFailed),
				errs.D("service_task_name", st.Name()),
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

// execWorkerOutcome applies the worker's stashed terminal outcome on resume
// (SRD-037 §3.5, reshaped SRD-038 §3.7): a completion binds the output; a
// Business Error raises a BPMN error caught by a boundary; a Business Status
// writes the WithStatus variable and completes; a terminal fault (the
// retries-exhausted case) fails the task. Classification now runs in the
// dispatcher before the loop, so the track only applies — it never sees a raw,
// unclassified fault. Runs on the track resume goroutine, so re's scope is
// available for the output/status write (no goroutine, §4.1).
func (st *ServiceTask) execWorkerOutcome(
	ctx context.Context,
	re renv.RuntimeEnvironment,
) ([]*flow.SequenceFlow, error) {
	wo := st.outcome

	switch wo.Kind() {
	case tasks.OutcomeBpmnError:
		code, message := wo.BpmnError()

		return st.raiseBpmnError(code, message)

	case tasks.OutcomeStatus:
		return st.writeStatus(ctx, re, wo.StatusValue())

	case tasks.OutcomeFault:
		return nil, st.technicalFault(wo.Fault())

	default: // OutcomeComplete
		return st.bindOutput(ctx, re, wo.Output())
	}
}

// bindOutput commits a completion's already-shaped output and advances. The
// output was mapped by the policy owner (the dispatcher under EngineAuthoritative,
// the worker under WorkerTrusted) before it reached the track (SRD-039 M8), so the
// track only commits it — no WithOutputMapping runs here anymore.
func (st *ServiceTask) bindOutput(
	ctx context.Context,
	re renv.RuntimeEnvironment,
	res []data.Data,
) ([]*flow.SequenceFlow, error) {
	if len(res) == 0 {
		return st.selectOutgoing(ctx, re)
	}

	if err := re.Put(res...); err != nil {
		return nil,
			errs.New(
				errs.M("couldn't commit worker result"),
				errs.C(errorClass),
				errs.E(err),
				errs.D("service_task_id", st.ID()))
	}

	return st.selectOutgoing(ctx, re)
}

// raiseBpmnError returns a *events.BpmnError as the resume error, so the track
// fails with it and the loop's matchErrorBoundary routes it to a matching Error
// boundary by code (SRD-037 FR-4). NewBpmnError errors only on an empty code —
// which a Business Error precludes — propagated defensively as a technical fault.
func (st *ServiceTask) raiseBpmnError(
	code, message string,
) ([]*flow.SequenceFlow, error) {
	var cause error
	if message != "" {
		cause = errors.New(message)
	}

	be, err := events.NewBpmnError(code, cause)
	if err != nil {
		return nil,
			errs.New(
				errs.M("service task %q: invalid business-error code", st.Name()),
				errs.C(errorClass, errs.OperationFailed),
				errs.E(err),
				errs.D("service_task_id", st.ID()))
	}

	return nil, be
}

// writeStatus writes value to the WithStatus variable and completes normally. A
// Status outcome with no WithStatus configured is a runtime fault; overwrite=false
// with a pre-existing variable is a collision fault — never a silent clobber
// (SRD-037 FR-5).
func (st *ServiceTask) writeStatus(
	ctx context.Context,
	re renv.RuntimeEnvironment,
	value data.Value,
) ([]*flow.SequenceFlow, error) {
	if st.statusVar == "" {
		return nil,
			errs.New(
				errs.M("service task %q: a Status outcome needs WithStatus", st.Name()),
				errs.C(errorClass, errs.InvalidState),
				errs.D("service_task_id", st.ID()))
	}

	if !st.statusOverwrite {
		if _, err := re.Find(ctx, st.statusVar); err == nil {
			return nil,
				errs.New(
					errs.M("service task %q: status variable %q already exists "+
						"(overwrite=false)", st.Name(), st.statusVar),
					errs.C(errorClass, errs.InvalidState),
					errs.D("service_task_id", st.ID()),
					errs.D("status_var", st.statusVar))
		}
	}

	res := data.MustParameter(st.statusVar,
		data.MustItemAwareElement(
			data.MustItemDefinition(value), data.ReadyDataState))

	if err := re.Put(res); err != nil {
		return nil,
			errs.New(
				errs.M("couldn't write status variable %q", st.statusVar),
				errs.C(errorClass),
				errs.E(err),
				errs.D("service_task_id", st.ID()))
	}

	return st.selectOutgoing(ctx, re)
}

// technicalFault wraps a raw fault as the terminal ServiceTask failure — the
// retries-exhausted outcome the dispatcher escalates once its RetryPolicy is
// spent (SRD-038). A fault reaches the track only as this terminal; the
// technical-vs-business classification now runs in the dispatcher, before the
// loop, so the track never re-classifies here.
func (st *ServiceTask) technicalFault(fault tasks.Fault) error {
	return errs.New(
		errs.M("service task %q worker reported a technical fault", st.Name()),
		errs.C(errorClass, errs.OperationFailed),
		errs.E(fault.Cause),
		errs.D("service_task_id", st.ID()),
		errs.D("fault_code", fault.Code))
}

// ------------------ tasks.ExternalWorker interface ---------------------------

// WorkerTopic reports the external-worker topic and whether this ServiceTask is
// worker-dispatched. The instance loop diverts a worker-dispatched task to the
// wait-node park path; an in-process task (ok == false) runs its operation.
func (st *ServiceTask) WorkerTopic() (tasks.Topic, bool) {
	return st.workerTopic, st.workerTopic != ""
}

// BindJobInput binds the operation's input message from r (without executing),
// for the engine to build the enqueued job's payload at park time (SRD-036).
func (st *ServiceTask) BindJobInput(
	ctx context.Context,
	r service.DataReader,
) (*data.ItemDefinition, error) {
	return st.operation.BindInputOnly(ctx, r)
}

// WorkerConfig reports the ServiceTask's per-service outcome policy — its
// WithErrorMapper, WithRetryPolicy, and WithOutputMapping — for the engine to
// resolve (two-level, over the engine-wide defaults) and ship in the enqueued
// Job.Policy so the policy owner can classify / retry / map the outcome. ok ==
// false for an in-process task (tasks.WorkerConfig, SRD-038 §3.3, SRD-039 M8).
func (st *ServiceTask) WorkerConfig() (tasks.Policy, bool) {
	return tasks.Policy{
		ErrorMapper:   st.errorMapper,
		RetryPolicy:   st.retryPolicy,
		OutputMapping: st.outputMapping,
		Trust:         st.trustMode,
	}, st.workerTopic != ""
}

// ------------------ eventproc.EventProcessor interface -----------------------

// ProcessEvent receives the synthetic WorkerOutcome the instance loop delivers to
// the parked track and stashes it for Exec to classify + apply on resume (SRD-036
// §3.5, SRD-037 §3.5).
func (st *ServiceTask) ProcessEvent(
	_ context.Context,
	eDef flow.EventDefinition,
) error {
	wo, ok := eDef.(*tasks.WorkerOutcome)
	if !ok {
		return errs.New(
			errs.M("service task %q expects a worker-outcome event", st.ID()),
			errs.C(errorClass, errs.TypeCastingError),
			errs.D("service_task_id", st.ID()),
			errs.D("event_type", string(eDef.Type())))
	}

	st.outcome = wo

	return nil
}

// -----------------------------------------------------------------------------

// interface check
var (
	_ exec.NodeExecutor        = (*ServiceTask)(nil)
	_ eventproc.EventProcessor = (*ServiceTask)(nil)
	_ tasks.ExternalWorker     = (*ServiceTask)(nil)
)
