package activities

import "github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"

// SendTask is a simple Task that is designed to send a Message to an
// external Participant (relative to the Process). Once the Message has been
// sent, the Task is completed.
type SendTask struct {
	Message        *bpmncommon.Message
	Implementation string
	task
}
