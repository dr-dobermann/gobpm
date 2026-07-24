package activities

import (
	"context"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/exec"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/msgflow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/renv"
)

// SendTask is a simple Task that is designed to send a Message to an
// external Participant (relative to the Process). Once the Message has been
// sent, the Task is completed.
type SendTask struct {
	message        *bpmncommon.Message
	correlationKey *bpmncommon.CorrelationKey
	implementation string
	task
}

// NewSendTask builds a SendTask that sends msg to the engine's MessageBroker
// (ADR-014 v.1). A nil msg is rejected.
func NewSendTask(
	name string,
	msg *bpmncommon.Message,
	taskOpts ...options.Option,
) (*SendTask, error) {
	name = strings.TrimSpace(name)
	if err := errs.CheckStr(
		name, "empty name isn't allowed for the SendTask",
		errorClass,
	); err != nil {
		return nil, err
	}

	if msg == nil {
		return nil,
			errs.New(
				errs.M("a message should be provided for the SendTask"),
				errs.C(errorClass, errs.EmptyNotAllowed))
	}

	// Separate the SendTask-specific options (e.g. WithCorrelationKey) from the
	// embedded task's options before building the task.
	var sc sndTaskConfig

	baseOpts := make([]options.Option, 0, len(taskOpts))
	for _, o := range taskOpts {
		if sto, ok := o.(SndTaskOption); ok {
			sto(&sc)

			continue
		}

		baseOpts = append(baseOpts, o)
	}

	t, err := newTask(name, baseOpts...)
	if err != nil {
		return nil, err
	}

	return &SendTask{
			task:           *t,
			message:        msg,
			correlationKey: sc.correlationKey,
		},
		nil
}

// CorrelationKey returns the CorrelationKey this SendTask stamps onto its
// outgoing message, or nil for name-match only (ADR-016 v.1 §2.2).
func (st *SendTask) CorrelationKey() *bpmncommon.CorrelationKey {
	return st.correlationKey
}

// Message returns the message the task sends.
func (st *SendTask) Message() *bpmncommon.Message {
	return st.message
}

// MessageToSend returns the message the task publishes. Implements
// msgflow.MessageProducer.
func (st *SendTask) MessageToSend() *bpmncommon.Message {
	return st.message
}

// Implementation returns the technology used to send the message (empty until
// a sending technology beyond the broker is wired).
func (st *SendTask) Implementation() string {
	return st.implementation
}

// Node returns the task as a flow.Node.
func (st *SendTask) Node() flow.Node {
	return st
}

// Clone returns a deep copy of the SendTask as a flow.Node.
func (st *SendTask) Clone() (flow.Node, error) {
	t, err := st.clone()
	if err != nil {
		return nil, err
	}

	msg, err := st.message.Clone()
	if err != nil {
		return nil, err
	}

	return &SendTask{
		task:           t,
		implementation: st.implementation,
		message:        msg,
		correlationKey: st.correlationKey,
	}, nil
}

// TaskType returns the BPMN task type.
func (st *SendTask) TaskType() flow.TaskType {
	return flow.SendTask
}

// Exec sends the task's message to the broker (ADR-014 v.1) and completes,
// returning the task's outgoing sequence flows.
func (st *SendTask) Exec(
	ctx context.Context,
	re renv.RuntimeEnvironment,
) ([]*flow.SequenceFlow, error) {
	if re == nil {
		return nil,
			errs.New(
				errs.M("no runtime environment"),
				errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if err := msgflow.Publish(ctx, re, st); err != nil {
		return nil,
			errs.New(
				errs.M("send task message publication failed"),
				errs.C(errorClass),
				errs.E(err),
				errs.D("send_task_name", st.Name()),
				errs.D("send_task_id", st.ID()))
	}

	return st.selectOutgoing(ctx, re)
}

var (
	_ exec.NodeExecutor       = (*SendTask)(nil)
	_ msgflow.MessageProducer = (*SendTask)(nil)
)
