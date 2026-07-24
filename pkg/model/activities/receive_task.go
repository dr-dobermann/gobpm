package activities

import (
	"context"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/eventproc"
	"github.com/dr-dobermann/gobpm/pkg/exec"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/msgflow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/renv"
)

// ReceiveTask is a simple Task that is designed to wait for a Message to
// arrive from an external Participant (relative to the Process). Once the
// Message has been received, the Task is completed.
// The actual Participant from which the Message is received can be identified
// by connecting the Receive Task to a Participant using a Message Flows within
// the definitional Collaboration of the Process.
// A Receive Task is often used to start a Process. In a sense, the Process is
// bootstrapped by the receipt of the Message. In order for the Receive Task to
// instantiate the Process its instantiate attribute MUST be set to true and it
// MUST NOT have any incoming Sequence Flow.
//
// ReceiveTask plugs into the engine's event wait/resume loop: it is a
// flow.EventNode whose single MessageEventDefinition the track registers (so
// the track parks in TrackWaitForEvent and a MessageWaiter subscribes the
// broker), and it is an eventproc.EventProcessor that captures the arrived
// payload on fire; the captured datum is bound into scope on resume by Exec
// (ADR-014 v.1).
type ReceiveTask struct {
	message        *bpmncommon.Message
	eDef           *events.MessageEventDefinition
	received       *data.ItemDefinition
	implementation string
	task
	instantiate bool
}

// NewReceiveTask builds a ReceiveTask that waits for msg. A nil msg is rejected.
func NewReceiveTask(
	name string,
	msg *bpmncommon.Message,
	taskOpts ...options.Option,
) (*ReceiveTask, error) {
	name = strings.TrimSpace(name)
	if err := errs.CheckStr(
		name, "empty name isn't allowed for the ReceiveTask",
		errorClass,
	); err != nil {
		return nil, err
	}

	if msg == nil {
		return nil,
			errs.New(
				errs.M("a message should be provided for the ReceiveTask"),
				errs.C(errorClass, errs.EmptyNotAllowed))
	}

	// Separate the ReceiveTask-specific options (e.g. WithInstantiate) from the
	// embedded task's options before building the task.
	var rc rcvTaskConfig

	baseOpts := make([]options.Option, 0, len(taskOpts))
	for _, o := range taskOpts {
		if rto, ok := o.(RcvTaskOption); ok {
			rto(&rc)

			continue
		}

		baseOpts = append(baseOpts, o)
	}

	t, err := newTask(name, baseOpts...)
	if err != nil {
		return nil, err
	}

	eDef, err := events.NewMessageEventDefinition(msg, nil)
	if err != nil {
		return nil, err
	}

	return &ReceiveTask{
			task:        *t,
			message:     msg,
			eDef:        eDef,
			instantiate: rc.instantiate,
		},
		nil
}

// Message returns the message the task waits for.
func (rt *ReceiveTask) Message() *bpmncommon.Message {
	return rt.message
}

// ExpectedMessage returns the message the task waits for. Implements
// msgflow.MessageConsumer.
func (rt *ReceiveTask) ExpectedMessage() *bpmncommon.Message {
	return rt.message
}

// Implementation returns the technology used to receive the message (empty
// until a receiving technology beyond the broker is wired).
func (rt *ReceiveTask) Implementation() string {
	return rt.implementation
}

// Instantiate reports whether the task instantiates the process on receipt
// (deferred — ADR-014 v.1 §2.7; always false in phase-1).
func (rt *ReceiveTask) Instantiate() bool {
	return rt.instantiate
}

// Node returns the task as a flow.Node.
func (rt *ReceiveTask) Node() flow.Node {
	return rt
}

// TaskType returns the BPMN task type.
func (rt *ReceiveTask) TaskType() flow.TaskType {
	return flow.ReceiveTask
}

// Clone returns a per-instance copy of the ReceiveTask as a flow.Node. The
// captured payload is per-instance runtime state and is not carried over.
func (rt *ReceiveTask) Clone() (flow.Node, error) {
	t, err := rt.clone()
	if err != nil {
		return nil, err
	}

	clonedMsg, err := rt.message.Clone()
	if err != nil {
		return nil, err
	}

	eDef, err := events.NewMessageEventDefinition(clonedMsg, nil)
	if err != nil {
		return nil, err
	}

	return &ReceiveTask{
		task:           t,
		implementation: rt.implementation,
		instantiate:    rt.instantiate,
		message:        clonedMsg,
		eDef:           eDef,
	}, nil
}

// Definitions returns the task's single message event definition, so the track
// registers it and parks waiting for the message. Implements flow.EventNode.
func (rt *ReceiveTask) Definitions() []flow.EventDefinition {
	return []flow.EventDefinition{rt.eDef}
}

// EventClass classifies the receive as an intermediate (mid-process) wait.
// Implements flow.EventNode.
func (rt *ReceiveTask) EventClass() flow.EventClass {
	return flow.IntermediateEventClass
}

// ProcessEvent captures the payload carried by the fired event definition so
// Exec can bind it into scope on resume. Implements eventproc.EventProcessor;
// ReceiveTask is the first model node to handle a fired event.
func (rt *ReceiveTask) ProcessEvent(
	_ context.Context,
	eDef flow.EventDefinition,
) error {
	rt.received = msgflow.CaptureItem(eDef)

	return nil
}

// Exec binds the received message payload into the execution scope (re.Put;
// the inherited task.UploadData then pushes it through the output associations)
// and completes, returning the task's outgoing sequence flows.
func (rt *ReceiveTask) Exec(
	ctx context.Context,
	re renv.RuntimeEnvironment,
) ([]*flow.SequenceFlow, error) {
	if re == nil {
		return nil,
			errs.New(
				errs.M("no runtime environment"),
				errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if err := msgflow.Bind(ctx, re, rt.received); err != nil {
		return nil,
			errs.New(
				errs.M("couldn't bind the received message payload"),
				errs.C(errorClass),
				errs.E(err),
				errs.D("receive_task_name", rt.Name()),
				errs.D("receive_task_id", rt.ID()))
	}

	return rt.selectOutgoing(ctx, re)
}

var (
	_ exec.NodeExecutor        = (*ReceiveTask)(nil)
	_ flow.EventNode           = (*ReceiveTask)(nil)
	_ eventproc.EventProcessor = (*ReceiveTask)(nil)
	_ msgflow.MessageConsumer  = (*ReceiveTask)(nil)
)
