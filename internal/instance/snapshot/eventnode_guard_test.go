package snapshot_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/stretchr/testify/require"
)

// pseudoEventNode reports itself as an event node without implementing
// flow.EventNode — the shape snapshot.New's type-assertion guard exists for.
// No real element can be both, so the guard is unreachable through the model's
// own constructors; it is reachable through Process.Add, which accepts any
// flow.Element, and that is exactly the substitution it defends against.
type pseudoEventNode struct{ *flow.BaseNode }

func (pseudoEventNode) NodeType() flow.NodeType { return flow.EventNodeType }

// Clone returns the stub itself. BaseNode's Clone panics by design (each
// concrete node implements its own), and the snapshot builder clones every node
// before it reaches the guard under test.
func (p pseudoEventNode) Clone() (flow.Node, error) { return p, nil }

// TestSnapshotRejectsPseudoEventNode covers the guard, whose error names the
// offending node — without it the snapshot would carry a node the engine would
// later assert on, failing far from the process that introduced it.
func TestSnapshotRejectsPseudoEventNode(t *testing.T) {
	p, err := process.New("pseudo-event")
	require.NoError(t, err)

	bn, err := flow.NewBaseNode("looks-like-an-event")
	require.NoError(t, err)

	require.NoError(t, p.Add(pseudoEventNode{bn}))

	s, err := snapshot.New(p)
	require.Error(t, err)
	require.Nil(t, s)
	require.ErrorContains(t, err, "EventNode")
}
