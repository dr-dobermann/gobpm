package activities

import (
	"github.com/dr-dobermann/gobpm/pkg/model/common"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
)

// A Send Task is a simple Task that is designed to send a Message to an
// external Participant (relative to the Process). Once the Message has been
// sent, the Task is completed.
type SendTask struct {
	task

	// This attribute specifies the technology that will be used to send and
	// receive the Messages. Valid values are "##unspecified" for leaving the
	// implementation technology open, "##WebService" for the Web service
	// technology or a URI identifying any other technology or coordination
	// protocol A Web service is the default technology.
	Implementation string

	// A Message for the messageRef attribute MAY be entered. This indicates
	// that the Message will be sent by the Task. The Message in this context
	// is equivalent to an out-only message pattern (Web service). One or more
	// corresponding outgoing Message Flows MAY be shown on the diagram.
	// However, the display of the Message Flows is NOT REQUIRED. The Message
	// is applied to all outgoing Message Flows and the Message will be sent down
	// all outgoing Message Flows at the completion of a single instance of the
	// Task.
	Message *common.Message

	// This attribute specifies the operation that is invoked by the
	// Send Task.
	Operation *service.Operation
}
