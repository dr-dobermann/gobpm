package executor

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/model"
	"github.com/dr-dobermann/gobpm/pkg/excenv"
)

// NodeExecutor should be implemented by every Node to make it
// possible to execute a node on the instance track
type NodeExecutor interface {

	// Exec runs single node and returns its valid
	// output sequence flows on success or error on a trouble
	Exec(ctx context.Context,
		eEnv excenv.ExecutionEnvironment) ([]*model.SequenceFlow, error)
}

func GetNodeExecutor(n model.Node) (NodeExecutor, error) {
	switch cn := n.(type) {
	case model.TaskModel:
		return GetTaskExecutor(cn)

	// case model.GatewayModel:
	// case model.EtEvent:
	default:
		return nil, fmt.Errorf("invalid node type: %T", cn)
	}
}
