package exec

import (
	"context"

	"github.com/dr-dobermann/gobpm/internal/renv"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// NodeExecutor is an interface for executing single flowNode.
type NodeExecutor interface {
	// Exec runs single node and returns its valid
	// output sequence flows on success or error on failure.
	Exec(
		ctx context.Context,
		re renv.RuntimeEnvironment,
	) ([]*flow.SequenceFlow, error)
}
