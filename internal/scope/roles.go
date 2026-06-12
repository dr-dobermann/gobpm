package scope

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// The node data protocol (ADR-010, SRD-007):
//
//  1. The track opens an execution Frame for the node on the instance's
//     data plane (Scope).
//
//  2. If the node consumes data, its NodeDataConsumer.LoadData instantiates
//     the node's input/property instances in the Frame and fills the inputs
//     from the incoming data associations.
//
//  3. The node executes against its per-execution environment: reads resolve
//     frame-first and walk the container scopes; results go to the Frame
//     (Put or output instances).
//
//  4. If the node produces data, its NodeDataProducer.UploadData fills the
//     output instances and pushes the outgoing data associations; the track
//     then commits the Frame atomically. On failure the Frame is discarded —
//     the container scope observes nothing.

// NodeDataConsumer is implemented by nodes that consume data: LoadData
// instantiates the node's inputs and properties in the execution Frame and
// fills the inputs from the node's incoming data associations. It is called
// by the track before the node executes.
type NodeDataConsumer interface {
	flow.Node

	// LoadData loads the node's data into the execution Frame.
	LoadData(context.Context, *Frame) error
}

// NodeDataProducer is implemented by nodes that produce data: UploadData
// fills the node's output instances in the execution Frame and pushes the
// outgoing data associations. It is called by the track after a successful
// node execution, right before the Frame commit.
type NodeDataProducer interface {
	flow.Node

	// UploadData yields the node's outputs through the execution Frame.
	UploadData(context.Context, *Frame) error
}
