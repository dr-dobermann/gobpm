package executor

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/common"
)

// NodeExecutor should be implemented by every Node to make it
// possible to execute a node on the instance's track
type NodeExecutor interface {
	// Exec runs single node and returns its valid
	// output sequence flows on success or error on an issue
	Exec(
		ctx context.Context,
		eEnv ExecutionEnvironment) ([]*common.SequenceFlow, error)
}

// Prologue checks the right condition to start node execution
// if the node provides a NodePrologue, then Prologue should start
// _before_ the node Exec.
// And only if Prologue returns nil error, it's possible to Exec be called.
type NodePrologue interface {
	Prologue(
		ctx context.Context,
		eEvn ExecutionEnvironment) error
}

// if the node provides NodeEpilogue, then Epilogue should be
// called after _successful_ Exec call.
type NodeEpliogue interface {
	Epilogue(
		ctx context.Context,
		eEnv ExecutionEnvironment) error
}
