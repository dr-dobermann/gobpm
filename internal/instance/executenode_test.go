package instance

import (
	"context"
	"fmt"
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/renv"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/stretchr/testify/require"
)

// failNode is an executable node whose data roles or execution fail on
// demand — it drives the executeNode failure branches (frame discarded at
// every stage, SRD-007 FR-4).
type failNode struct {
	*flow.BaseNode

	failLoad   bool
	failExec   bool
	failUpload bool
}

func (fn *failNode) SupportOutgoingFlow(*flow.SequenceFlow) error { return nil }
func (fn *failNode) AcceptIncomingFlow(*flow.SequenceFlow) error  { return nil }
func (fn *failNode) Node() flow.Node                              { return fn }

func (fn *failNode) Exec(
	_ context.Context,
	_ renv.RuntimeEnvironment,
) ([]*flow.SequenceFlow, error) {
	if fn.failExec {
		return nil, fmt.Errorf("exec failed by design")
	}

	return nil, nil
}

func (fn *failNode) LoadData(_ context.Context, _ *scope.Frame) error {
	if fn.failLoad {
		return fmt.Errorf("load failed by design")
	}

	return nil
}

func (fn *failNode) UploadData(_ context.Context, _ *scope.Frame) error {
	if fn.failUpload {
		return fmt.Errorf("upload failed by design")
	}

	return nil
}

// TestExecuteNodeFailureStages drives executeNode through a consumer
// failure, an executor failure, and a producer failure — each aborts the
// step and the deferred Discard leaves the container scope untouched.
func TestExecuteNodeFailureStages(t *testing.T) {
	_ = data.CreateDefaultStates()

	inst, err := New(buildForkSnapshot(t), scope.EmptyDataPath,
		enginert.Default(), mockeventproc.NewMockEventProducer(t), nil)
	require.NoError(t, err)

	newNode := func(name string) *flow.BaseNode {
		bn, err := flow.NewBaseNode(name)
		require.NoError(t, err)

		return bn
	}

	cases := []struct {
		name string
		node *failNode
		ok   bool
	}{
		{"consumer failure", &failNode{
			BaseNode: newNode("fail-load"), failLoad: true}, false},
		{"executor failure", &failNode{
			BaseNode: newNode("fail-exec"), failExec: true}, false},
		{"producer failure", &failNode{
			BaseNode: newNode("fail-upload"), failUpload: true}, false},
		{"all stages pass", &failNode{
			BaseNode: newNode("all-good")}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr, err := newTrack(tc.node, inst, nil)
			require.NoError(t, err)

			_, err = tr.executeNode(context.Background(),
				&stepInfo{node: tc.node})

			if tc.ok {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
