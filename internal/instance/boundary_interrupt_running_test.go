package instance

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
)

// SRD-029 M3c — interrupting a RUNNING activity via the §3.7 checkpoint. A boundary
// fire cancels the per-track context handed to ne.Exec; the post-Exec checkpoint
// discards the result (never commits, never fails) and the loop runs the exception
// flow. These drive a real ServiceTask whose gooper op the test controls.

// guardedServiceInstance builds start -> host(ServiceTask, op) -> normalEnd, with an
// interrupting signal boundary on host -> excEnd. It returns the instance, the two
// end-node ids (to tell the interrupted normal path from the exception path), and the
// signal definition the boundary catches.
func guardedServiceInstance(
	t *testing.T,
	ep eventproc.EventProducer,
	op service.Operation,
) (inst *Instance, normalEndID, excEndID string, sigDef flow.EventDefinition) {
	t.Helper()

	_ = data.CreateDefaultStates()

	p, err := process.New("srd029-m3c")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	host, err := activities.NewServiceTask("host", op, activities.WithoutParams())
	require.NoError(t, err)

	normalEnd, err := events.NewEndEvent("normal-end")
	require.NoError(t, err)

	sig, err := events.NewSignal("sigR", nil)
	require.NoError(t, err)

	def, err := events.NewSignalEventDefinition(sig)
	require.NoError(t, err)

	be, err := events.NewBoundaryEvent("bndR", host, def, true)
	require.NoError(t, err)

	excEnd, err := events.NewEndEvent("exc-end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, host, normalEnd, be, excEnd} {
		require.NoError(t, p.Add(e))
	}

	_, err = flow.Link(start, host)
	require.NoError(t, err)
	_, err = flow.Link(host, normalEnd)
	require.NoError(t, err)
	_, err = flow.Link(be, excEnd)
	require.NoError(t, err)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	inst, err = New(s, scope.EmptyDataPath, enginert.Default(), ep, nil)
	require.NoError(t, err)

	return inst, normalEnd.ID(), excEnd.ID(), def
}

// fireWhenRunning runs inst, waits until the op has started and the boundary is armed,
// then fires the boundary through its watch (exactly as the hub would on the signal).
func fireWhenRunning(
	t *testing.T,
	inst *Instance,
	ep *recordingProducer,
	started <-chan struct{},
	sigDef flow.EventDefinition,
) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	require.NoError(t, inst.Run(ctx))

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("the service operation did not start")
	}

	require.Eventually(t, func() bool { return ep.capturedWatch() != nil },
		2*time.Second, 5*time.Millisecond, "the boundary must arm over the running activity")

	require.NoError(t, ep.capturedWatch().ProcessEvent(ctx, sigDef))
}

// T-5: an interrupting boundary cancels a ctx-honouring activity mid-Exec; the op
// returns on ctx.Done, the §3.7 checkpoint discards it, and the exception flow runs.
func TestInterruptingBoundaryInterruptsRunningActivity(t *testing.T) {
	started := make(chan struct{})

	op, err := gooper.New("blocking-honour",
		func(ctx context.Context, _ service.DataReader,
			_ *data.ItemDefinition,
		) (*data.ItemDefinition, error) {
			close(started)
			<-ctx.Done() // honour cancellation

			return nil, ctx.Err()
		})
	require.NoError(t, err)

	ep := &recordingProducer{}
	inst, normalEndID, excEndID, sigDef := guardedServiceInstance(t, ep, op)

	fireWhenRunning(t, inst, ep, started, sigDef)

	require.Eventually(t, func() bool { return inst.State() == Completed },
		2*time.Second, 5*time.Millisecond,
		"the instance completes via the exception flow after the interrupt")
	require.NoError(t, inst.LastErr())

	require.True(t, hasCanceledTrack(inst), "the interrupted host track is cancelled")
	require.True(t, reachedNode(inst, excEndID), "the exception flow ran")
	require.False(t, reachedNode(inst, normalEndID),
		"the interrupted activity's normal path did not run")
}

// T-6: a ctx-IGNORING activity that returns success after the cancel still has its
// result discarded — the §3.7 check is on ctx.Err(), not the returned error, so the
// op's normal outgoing flow is never followed (cancellation wins over success).
func TestInterruptingBoundaryDiscardsIgnoredCtxResult(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})

	op, err := gooper.New("blocking-ignore",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition,
		) (*data.ItemDefinition, error) {
			close(started)
			<-release // ignore ctx — return only when the test releases

			return nil, nil // success, despite the cancelled context
		})
	require.NoError(t, err)

	ep := &recordingProducer{}
	inst, normalEndID, excEndID, sigDef := guardedServiceInstance(t, ep, op)

	fireWhenRunning(t, inst, ep, started, sigDef)

	// fireBoundary cancels the host context BEFORE it disarms the watch, so once the
	// unregister is observed the cancel has landed. Only then release the op — it
	// returns success while the context is already done, and the checkpoint must
	// still discard the result (the op never looks at ctx).
	require.Eventually(t, func() bool { return len(ep.unregisteredDefs()) > 0 },
		2*time.Second, 5*time.Millisecond, "the fire must cancel the host before release")

	close(release)

	require.Eventually(t, func() bool { return inst.State() == Completed },
		2*time.Second, 5*time.Millisecond,
		"the instance completes after the ignored-ctx op is discarded")
	require.NoError(t, inst.LastErr())

	require.True(t, hasCanceledTrack(inst), "the host track is cancelled, not failed")
	require.True(t, reachedNode(inst, excEndID), "the exception flow ran")
	require.False(t, reachedNode(inst, normalEndID),
		"the discarded result did not follow the activity's normal flow")
}
