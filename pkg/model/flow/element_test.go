package flow_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/stretchr/testify/require"
)

func TestElementTypes(t *testing.T) {
	require.Error(t, flow.NodeType("invalid_node_type").Validate())
	require.Error(t, flow.ElementType("unknown_type").Validate())
	require.NoError(t, flow.NodeElement.Validate())
	require.Error(t, flow.ValidateNodeTypes(flow.ActivityNodeType, flow.EventNodeType, "unknown_typw"))
}

func TestBaseElementBindUnbind(t *testing.T) {
	be, err := flow.NewBaseElement("be")
	require.NoError(t, err)

	procA, err := process.New("procA")
	require.NoError(t, err)
	procB, err := process.New("procB")
	require.NoError(t, err)

	// a nil container is rejected.
	require.Error(t, be.BindTo(nil))
	require.Nil(t, be.Container())

	// binding succeeds and is reflected by Container().
	require.NoError(t, be.BindTo(procA))
	require.Equal(t, procA.ID(), be.Container().ID())

	// re-binding to the same container is a no-op success.
	require.NoError(t, be.BindTo(procA))

	// binding to a different container is rejected.
	require.Error(t, be.BindTo(procB))

	// unbinding succeeds; a second unbind fails (no container left).
	require.NoError(t, be.Unbind())
	require.Nil(t, be.Container())
	require.Error(t, be.Unbind())
}

func TestBaseElementETypePanics(t *testing.T) {
	be, err := flow.NewBaseElement("be")
	require.NoError(t, err)

	// EType has no meaning on a generic BaseElement — each concrete element
	// implements its own; the generic one panics.
	require.Panics(t, func() { _ = be.EType() })
}

func TestNewBaseElementError(t *testing.T) {
	// a failing base option (empty explicit id) is propagated.
	_, err := flow.NewBaseElement("be", foundation.WithID("  "))
	require.Error(t, err)
}
