package instance

import (
	"context"
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

// buildLinearSnapshot builds a minimal start -> end process (one outgoing
// flow, no fork): enough to exercise the event loop end to end.
func buildLinearSnapshot(t *testing.T) *snapshot.Snapshot {
	t.Helper()

	p, err := process.New("m2-linear")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, end} {
		require.NoError(t, p.Add(e))
	}

	_, err = flow.Link(start, end)
	require.NoError(t, err)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	return s
}

// TestM2LinearCompletes verifies the event loop drives a single track to
// completion: the loop spawns the initial track, the track ends, the instance
// reaches Finished via the active-track count, and no goroutine leaks. Under
// -race it also exercises that instance lifecycle state (state, registry) is
// mutated only in the loop goroutine while State()/trackCount are read
// concurrently. (The fork / evSpawn path is exercised in M4, once token.split
// is removed — it currently panics on a >1 split, a pre-existing bug.)
func TestM2LinearCompletes(t *testing.T) {
	_ = data.CreateDefaultStates()

	s := buildLinearSnapshot(t)
	ep := mockeventproc.NewMockEventProducer(t)

	inst, err := New(s, nil, ep, nil)
	require.NoError(t, err)

	leak := assertNoGoroutineLeak(t)

	ctx, cancel := context.WithCancel(context.Background())

	require.NoError(t, inst.Run(ctx))

	require.Eventually(t,
		func() bool { return inst.State() == Finished },
		2*time.Second, 5*time.Millisecond,
		"instance should reach Finished once its single track ends")

	require.EqualValues(t, 1, inst.trackCount.Load())

	cancel()
	leak()
}
