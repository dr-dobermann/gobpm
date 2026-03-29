package activities

import (
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
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
type ReceiveTask struct {
	Operation      service.Operation
	Message        *bpmncommon.Message
	Implementation string
	task
	Instantiate bool
}
