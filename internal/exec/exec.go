package exec

import (
	"context"

	"github.com/dr-dobermann/gobpm/internal/renv"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

type NodeExecutor interface {
	// Exec runs single node and returns its valid
	// output sequence flows on success or error on failure.
	Exec(
		ctx context.Context,
		re renv.RuntimeEnvironment,
	) ([]*flow.SequenceFlow, error)
}

// Prologue checks the right condition to start node execution
// if the node provides a NodePrologue, then Prologue should start
// _before_ the node Exec.
// Node Exec starts only if the Prologue returns no error.
type NodePrologue interface {
	Prologue(
		ctx context.Context,
		re renv.RuntimeEnvironment) error
}

// if the node provides NodeEpilogue, then Epilogue should be
// called after _successful_ Exec call.
type NodeEpliogue interface {
	Epilogue(
		ctx context.Context,
		re renv.RuntimeEnvironment) error
}
