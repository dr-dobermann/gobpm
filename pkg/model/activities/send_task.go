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

	t, err := newTask(name, taskOpts...)
	if err != nil {
		return nil, err
	}

	return &SendTask{
			task:    *t,
			message: msg,
		},
		nil
}

// Message returns the message the task sends.
func (st *SendTask) Message() *bpmncommon.Message {
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
func (st *SendTask) Clone() flow.Node {
	return &SendTask{
		task:           st.clone(),
		implementation: st.implementation,
		message:        st.message.Clone(),
	}
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

	if err := msgflow.Send(ctx, re, st.message); err != nil {
		return nil,
			errs.New(
				errs.M("send task message publication failed"),
				errs.C(errorClass),
				errs.E(err),
				errs.D("send_task_name", st.Name()),
				errs.D("send_task_id", st.ID()))
	}

	return st.Outgoing(), nil
}

var (
	_ exec.NodeExecutor = (*SendTask)(nil)
)
