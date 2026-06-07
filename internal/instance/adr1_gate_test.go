package instance

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/stretchr/testify/require"
)

// TestTerminationCascade verifies ADR-001 §7 termination cascade at the runtime
// level: cancelling the instance context stops every track and drains its
// goroutine within a bound, leaving the instance in a terminal state. The BPMN
// Terminate End Event node that triggers this cascade is owned by the Events
// ADR; this test covers the ctx.Done() cascade the runtime implements.
func TestTerminationCascade(t *testing.T) {
	_ = data.CreateDefaultStates()

	s := buildForkSnapshot(t)
	ep := mockeventproc.NewMockEventProducer(t)

	inst, err := New(s, nil, ep, nil)
	require.NoError(t, err)

	leak := assertNoGoroutineLeak(t)

	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, inst.Run(ctx))

	cancel()

	require.Eventually(t,
		func() bool {
			st := inst.State()
			return st == Completed || st == Terminated
		},
		time.Second, 5*time.Millisecond,
		"instance reaches a terminal state after the ctx cascade")

	// the cascade drains every track goroutine back to baseline (no leak).
	leak()
}
