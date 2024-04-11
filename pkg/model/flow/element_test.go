package flow_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/stretchr/testify/require"
)

func TestElementTypes(t *testing.T) {
	require.Error(t, flow.NodeType("invalid_node_type").Validate())
	require.Error(t, flow.ElementType("unknown_type").Validate())
	require.NoError(t, flow.NodeElement.Validate())
	require.Error(t, flow.ValidateNodeTypes(flow.ActivityNodeType, flow.EventNodeType, "unknown_typw"))
}
