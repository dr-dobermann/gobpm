package instance

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/stretchr/testify/require"
)

// TestParallelSplitCompletes verifies the Parallel gateway split (SRD-005 M2):
// the gateway's Exec returns all outgoing flows, the existing fork machinery
// builds one track per extra flow, and both branches reach their own End.
//
//	start ─> parallelGW ─┬─> end1
//	                     └─> end2
func TestParallelSplitCompletes(t *testing.T) {
	_ = data.CreateDefaultStates()

	p, err := process.New("parallel-split")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	pg, err := gateways.NewParallelGateway(
		gateways.WithDirection(gateways.Diverging))
	require.NoError(t, err)

	end1, err := events.NewEndEvent("end1")
	require.NoError(t, err)

	end2, err := events.NewEndEvent("end2")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, pg, end1, end2} {
		require.NoError(t, p.Add(e))
	}

	_, err = flow.Link(start, pg)
	require.NoError(t, err)
	_, err = flow.Link(pg, end1)
	require.NoError(t, err)
	_, err = flow.Link(pg, end2)
	require.NoError(t, err)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	ep := mockeventproc.NewMockEventProducer(t)

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(), ep, nil)
	require.NoError(t, err)

	leak := assertNoGoroutineLeak(t)
	defer leak()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, inst.Run(ctx))

	require.Eventually(t,
		func() bool { return inst.State() == Completed },
		2*time.Second, 5*time.Millisecond,
		"both split branches should complete")

	require.NoError(t, inst.LastErr())
	require.EqualValues(t, 2, inst.trackCount.Load(),
		"split over two outgoing flows -> 2 tracks")
	require.Empty(t, inst.GetTokens(), "no active tokens after completion")
}
