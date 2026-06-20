package instance

import (
	"bytes"
	"log/slog"
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

// TestLoopEventLogging verifies the loop emits a Debug line per track event when the
// configured logger is at Debug level (off by default, so normal runs stay quiet).
// This is the observability that makes a stuck join self-evident in the logs.
func TestLoopEventLogging(t *testing.T) {
	_ = data.CreateDefaultStates()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf,
		&slog.HandlerOptions{Level: slog.LevelDebug}))

	p, err := process.New("loop-log")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	split, err := gateways.NewParallelGateway()
	require.NoError(t, err)
	join, err := gateways.NewParallelGateway()
	require.NoError(t, err)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, split, join, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, split)
	link(t, split, join)
	link(t, split, join)
	link(t, join, end)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	inst, err := New(s, scope.EmptyDataPath,
		enginert.Default().WithLogger(logger),
		mockeventproc.NewMockEventProducer(t), nil)
	require.NoError(t, err)

	require.NoError(t, inst.Run(t.Context()))
	require.Eventually(t,
		func() bool { return inst.State() == Completed },
		2*time.Second, 5*time.Millisecond)

	out := buf.String()
	require.Contains(t, out, "track event")
	require.Contains(t, out, "kind=fork")
	require.Contains(t, out, "kind=ended")
}
