package thresher_test

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

// TestManualTaskPassThrough verifies a ManualTask flows straight through to
// completion — it parks nothing and waits for nothing (ADR-020 §2.10).
func TestManualTaskPassThrough(t *testing.T) {
	proc, err := process.New("mt-e2e")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	mt, err := activities.NewManualTask("ack", activities.WithoutParams())
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, mt, end} {
		require.NoError(t, proc.Add(e))
	}

	link(t, start, mt)
	link(t, mt, end)

	th, cancel := runEngine(t, proc)
	defer cancel()

	h, err := th.StartLatest(proc.ID())
	require.NoError(t, err)

	ctx, c := context.WithTimeout(context.Background(), 3*time.Second)
	defer c()

	state, err := h.WaitCompletion(ctx)
	require.NoError(t, err)
	require.Equal(t, thresher.StateCompleted, state)
}
