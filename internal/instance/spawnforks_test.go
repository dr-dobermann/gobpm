package instance

import (
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/stretchr/testify/require"
)

// plainNode is a flow.Node that is deliberately NOT an exec.NodeExecutor: its
// promoted *flow.BaseNode carries no Exec. It stands in for a malformed fork
// target so newTrack fails inside spawnForks (the defensive error path).
type plainNode struct {
	*flow.BaseNode
}

func (plainNode) SupportOutgoingFlow(*flow.SequenceFlow) error { return nil }
func (plainNode) AcceptIncomingFlow(*flow.SequenceFlow) error  { return nil }

// Node returns the plainNode itself (the embedded BaseNode panics on Node()) —
// a flow.Node that is not an exec.NodeExecutor.
func (p plainNode) Node() flow.Node { return p }

// TestSpawnForks drives the two branches of spawnForks that the happy-path fork
// tests do not reach: the newTrack build error (malformed target → record the
// error and stopAll) and the already-stopping case (a freshly forked track is
// stopped immediately).
func TestSpawnForks(t *testing.T) {
	_ = data.CreateDefaultStates()

	inst, err := New(buildForkSnapshot(t), nil, enginert.Default(),
		mockeventproc.NewMockEventProducer(t), nil)
	require.NoError(t, err)

	start, err := events.NewStartEvent("spawn-src")
	require.NoError(t, err)

	var spawned []*track

	spawn := func(tr *track) {
		spawned = append(spawned, tr)
		inst.tracks[tr.ID()] = tr
	}

	stopCalled := false
	stopAll := func() { stopCalled = true }

	t.Run("forked track stopped when instance is already stopping", func(t *testing.T) {
		end, err := events.NewEndEvent("spawn-end")
		require.NoError(t, err)

		fValid, err := flow.Link(start, end) // target is a real executor
		require.NoError(t, err)

		inst.spawnForks(trackEvent{flows: []*flow.SequenceFlow{fValid}},
			spawn, stopAll, true)

		require.Len(t, spawned, 1)
		require.True(t, spawned[0].stopIt.Load(),
			"a track forked while stopping must be stopped at once")
		require.False(t, stopCalled)
	})

	t.Run("newTrack build error records the error and stops the instance", func(t *testing.T) {
		bn, err := flow.NewBaseNode("plain")
		require.NoError(t, err)

		fBad, err := flow.Link(start, plainNode{bn}) // target lacks NodeExecutor
		require.NoError(t, err)

		inst.spawnForks(trackEvent{flows: []*flow.SequenceFlow{fBad}},
			spawn, stopAll, false)

		require.True(t, stopCalled, "a build error must trigger stopAll")
		require.Error(t, inst.LastErr())
		require.Len(t, spawned, 1, "no track is spawned on the error path")
	})
}
