package activities

import (
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
)

// SendTask is a simple Task that is designed to send a Message to an
// external Participant (relative to the Process). Once the Message has been
// sent, the Task is completed.
type SendTask struct {
	Message        *bpmncommon.Message
	Operation      *service.Operation
	Implementation string
	task
}
