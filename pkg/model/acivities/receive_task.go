package acivities

import (
	"github.com/dr-dobermann/gobpm/pkg/model/common"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
)

// A Receive Task is a simple Task that is designed to wait for a Message to
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
	Task

	// A Message for the messageRef attribute MAY be entered. This indicates that
	// the Message will be received by the Task. The Message in this context is
	// equivalent to an in-only message pattern (Web service). One (1) or more
	// corresponding incoming Message Flows MAY be shown on the diagram.
	// However, the display of the Message Flows is NOT REQUIRED. The Message is
	// applied to all incoming Message Flows, but can arrive for only one (1) of
	// the incoming Message Flows for a single instance of the Task.
	Message *common.Message

	// Receive Tasks can be defined as the instantiation mechanism for the
	// Process with the instantiate attribute. This attribute MAY be set to true
	// if the Task is the first Activity (i.e., there are no incoming Sequence
	// Flows). Multiple Tasks MAY have this attribute set to true.
	Instantiate bool

	// This attribute specifies the operation through which the Receive Task
	// receives the Message.
	Operation service.Operation

	// This attribute specifies the technology that will be used to send and
	// receive the Messages. Valid values are "##unspecified" for leaving the
	// implementation technology open, "##WebService" for the Web service
	// technology or a URI identifying any other technology or coordination
	// protocol A Web service is the default technology.
	Implementation string
}
