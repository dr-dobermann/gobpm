package instance

import (
	"context"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/stretchr/testify/require"
)

// buildForkSnapshot builds a process whose start node has two outgoing flows
// (a fork point):
//
//	start ─┬─> end1
//	       └─> end2
func buildForkSnapshot(t *testing.T) *snapshot.Snapshot {
	t.Helper()

	p, err := process.New("m4-fork")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	end1, err := events.NewEndEvent("end1")
	require.NoError(t, err)

	end2, err := events.NewEndEvent("end2")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, end1, end2} {
		require.NoError(t, p.Add(e))
	}

	_, err = flow.Link(start, end1)
	require.NoError(t, err)

	_, err = flow.Link(start, end2)
	require.NoError(t, err)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	return s
}

func nodeSet(p TokenPath) map[string]bool {
	out := map[string]bool{}
	for _, sv := range p.Steps {
		out[sv.Node.Name()] = true
	}

	return out
}

// TestM4ForkCompletes verifies the reworked fork: the loop builds a new track
// for the extra flow (no token.split), both tracks complete, and the path
// history reflects the lineage — the parent path forks into the child.
func TestM4ForkCompletes(t *testing.T) {
	_ = data.CreateDefaultStates()

	s := buildForkSnapshot(t)
	ep := mockeventproc.NewMockEventProducer(t)

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(), ep, nil)
	require.NoError(t, err)

	leak := assertNoGoroutineLeak(t)

	ctx, cancel := context.WithCancel(context.Background())

	require.NoError(t, inst.Run(ctx))

	require.Eventually(t,
		func() bool { return inst.State() == Completed },
		2*time.Second, 5*time.Millisecond,
		"both forked tracks should complete")

	require.NoError(t, inst.LastErr())
	require.EqualValues(t, 2, inst.trackCount.Load(), "one fork over two flows -> 2 tracks")
	require.Empty(t, inst.GetTokens(), "no active tokens after completion")

	hist := inst.TokenHistory()
	require.Len(t, hist, 2)

	var root, child TokenPath

	for _, p := range hist {
		if p.ParentID == "" {
			root = p
		} else {
			child = p
		}
	}

	require.NotEmpty(t, root.TrackID, "there is a root track")
	require.Equal(t, root.TrackID, child.ParentID, "the child forked from the root")
	require.Equal(t, TokenConsumed, root.Terminal)
	require.Equal(t, TokenConsumed, child.Terminal)

	rootNodes := nodeSet(root)
	childNodes := nodeSet(child)

	require.True(t, rootNodes["start"], "root visits the start node")
	require.False(t, childNodes["start"], "child branched after the start node")

	// across both paths, both ends are visited.
	ends := map[string]bool{}
	for n := range rootNodes {
		ends[n] = ends[n] || n == "end1" || n == "end2"
	}

	for n := range childNodes {
		ends[n] = ends[n] || n == "end1" || n == "end2"
	}

	require.True(t, ends["end1"] && ends["end2"], "both end nodes visited across the paths")

	cancel()
	leak()
}

// TestM4ForkRace hammers the projections from another goroutine while a
// forking instance runs — under -race it proves fork + lock-free reads compose.
func TestM4ForkRace(t *testing.T) {
	_ = data.CreateDefaultStates()

	s := buildForkSnapshot(t)
	ep := mockeventproc.NewMockEventProducer(t)

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(), ep, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})

	go func() {
		for {
			select {
			case <-done:
				return
			default:
				_ = inst.GetTokens()
				_ = inst.TokenHistory()
			}
		}
	}()

	require.NoError(t, inst.Run(ctx))
	require.Eventually(t,
		func() bool { return inst.State() == Completed },
		2*time.Second, 5*time.Millisecond)

	close(done)
}
