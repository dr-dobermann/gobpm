package thresher_test

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

// signalCatchProcess builds start -> catch(signal name) -> end.
func signalCatchProcess(t *testing.T, procID, name string) *process.Process {
	t.Helper()

	sig, err := events.NewSignal(name, nil)
	require.NoError(t, err)
	def, err := events.NewSignalEventDefinition(sig)
	require.NoError(t, err)

	catch, err := events.NewIntermediateCatchEvent("catch-"+name, def)
	require.NoError(t, err)
	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	proc, err := process.New(procID)
	require.NoError(t, err)
	for _, e := range []flow.Element{start, catch, end} {
		require.NoError(t, proc.Add(e))
	}
	link(t, start, catch)
	link(t, catch, end)

	return proc
}

// signalThrowProcess builds start -> throw(signal name) -> end.
func signalThrowProcess(t *testing.T, procID, name string) *process.Process {
	t.Helper()

	sig, err := events.NewSignal(name, nil)
	require.NoError(t, err)
	def, err := events.NewSignalEventDefinition(sig)
	require.NoError(t, err)

	throw, err := events.NewIntermediateThrowEvent("throw-"+name, def)
	require.NoError(t, err)
	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	proc, err := process.New(procID)
	require.NoError(t, err)
	for _, e := range []flow.Element{start, throw, end} {
		require.NoError(t, proc.Add(e))
	}
	link(t, start, throw)
	link(t, throw, end)

	return proc
}

// TestSignalCatchThrow verifies a thrown signal resumes a waiting catcher in
// another instance (FR-1, FR-3, FR-6).
func TestSignalCatchThrow(t *testing.T) {
	catcher := signalCatchProcess(t, "sc-catch", "GO")
	thrower := signalThrowProcess(t, "sc-throw", "GO")

	th, cancel := runEngine(t, catcher)
	defer cancel()
	_, err := th.RegisterProcess(thrower)
	require.NoError(t, err)

	ch, err := th.StartLatest(catcher.ID())
	require.NoError(t, err)

	time.Sleep(150 * time.Millisecond) // catcher reaches and parks on the catch

	_, err = th.StartLatest(thrower.ID()) // throws GO
	require.NoError(t, err)

	ctx, cc := context.WithTimeout(context.Background(), 3*time.Second)
	defer cc()
	st, err := ch.WaitCompletion(ctx)
	require.NoError(t, err)
	require.Equal(t, thresher.StateCompleted, st)
}

// TestSignalBroadcast verifies one throw fires EVERY catcher of the signal name
// across instances (FR-2, FR-3, NFR-1) — the broadcast canary.
func TestSignalBroadcast(t *testing.T) {
	catcher := signalCatchProcess(t, "sb-catch", "GO")
	thrower := signalThrowProcess(t, "sb-throw", "GO")

	th, cancel := runEngine(t, catcher)
	defer cancel()
	_, err := th.RegisterProcess(thrower)
	require.NoError(t, err)

	c1, err := th.StartLatest(catcher.ID())
	require.NoError(t, err)
	c2, err := th.StartLatest(catcher.ID())
	require.NoError(t, err)

	time.Sleep(150 * time.Millisecond) // both catchers park on the catch

	_, err = th.StartLatest(thrower.ID()) // one throw of GO
	require.NoError(t, err)

	ctx, cc := context.WithTimeout(context.Background(), 3*time.Second)
	defer cc()
	st1, err := c1.WaitCompletion(ctx)
	require.NoError(t, err)
	require.Equal(t, thresher.StateCompleted, st1)
	st2, err := c2.WaitCompletion(ctx)
	require.NoError(t, err)
	require.Equal(t, thresher.StateCompleted, st2)
}

// TestSignalThrownIntoVoid verifies a signal with no catcher is a no-op and is
// NOT buffered for a later catcher (FR-4, NFR-1 — the §2.4 no-store contract).
func TestSignalThrownIntoVoid(t *testing.T) {
	catcher := signalCatchProcess(t, "sv-catch", "GO")
	thrower := signalThrowProcess(t, "sv-throw", "GO")

	th, cancel := runEngine(t, thrower)
	defer cancel()
	_, err := th.RegisterProcess(catcher)
	require.NoError(t, err)

	_, err = th.StartLatest(thrower.ID()) // throw GO with no catcher → no-op
	require.NoError(t, err)

	time.Sleep(150 * time.Millisecond)

	ch, err := th.StartLatest(catcher.ID()) // starts AFTER the throw
	require.NoError(t, err)

	// The earlier signal is not retro-delivered: the catcher stays parked.
	ctx, cc := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cc()
	_, err = ch.WaitCompletion(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

// TestSignalSingleShotConsume verifies an intermediate catch consumes the signal
// once, and a later throw with no catcher is a clean no-op (FR-5).
func TestSignalSingleShotConsume(t *testing.T) {
	catcher := signalCatchProcess(t, "ss-catch", "GO")
	thrower := signalThrowProcess(t, "ss-throw", "GO")

	th, cancel := runEngine(t, catcher)
	defer cancel()
	_, err := th.RegisterProcess(thrower)
	require.NoError(t, err)

	ch, err := th.StartLatest(catcher.ID())
	require.NoError(t, err)

	time.Sleep(150 * time.Millisecond)

	_, err = th.StartLatest(thrower.ID()) // throw 1 → catcher fires
	require.NoError(t, err)

	ctx, cc := context.WithTimeout(context.Background(), 3*time.Second)
	defer cc()
	st, err := ch.WaitCompletion(ctx)
	require.NoError(t, err)
	require.Equal(t, thresher.StateCompleted, st)

	// The catch is consumed; a second throw finds no catcher → clean no-op.
	_, err = th.StartLatest(thrower.ID())
	require.NoError(t, err)
}

// sigDef builds a signal event definition for name — the broadcast match key.
func sigDef(t *testing.T, name string) *events.SignalEventDefinition {
	t.Helper()

	sig, err := events.NewSignal(name, nil)
	require.NoError(t, err)
	def, err := events.NewSignalEventDefinition(sig)
	require.NoError(t, err)

	return def
}

// signalStartProcess builds a process whose start is a signal StartEvent (no
// incoming) on sigName: start(signal) → marker(ServiceTask) → end. A broadcast of
// sigName instantiates it (SRD-026 FR-4); the marker pushes onto done when it runs.
func signalStartProcess(
	t *testing.T, procID, sigName, marker string, done chan<- string,
) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	start, err := events.NewStartEvent("start",
		events.WithSignalTrigger(sigDef(t, sigName)))
	require.NoError(t, err)

	mark := ebMarkerService(t, "mark-"+procID, marker, done)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	proc, err := process.New(procID)
	require.NoError(t, err)

	for _, e := range []flow.Element{start, mark, end} {
		require.NoError(t, proc.Add(e))
	}

	link(t, start, mark)
	link(t, mark, end)

	return proc
}

// TestSignalStartInstantiates: a broadcast signal instantiates a process whose
// start trigger is a signal StartEvent (SRD-026 FR-4).
func TestSignalStartInstantiates(t *testing.T) {
	done := make(chan string, 4)
	proc := signalStartProcess(t, "sig-start", "GO", "X", done)

	th, cancel := runEngine(t, proc)
	defer cancel()

	require.NoError(t, th.PropagateEvent(context.Background(), sigDef(t, "GO")))

	select {
	case got := <-done:
		require.Equal(t, "X", got, "the signal-start instance runs")
	case <-time.After(3 * time.Second):
		t.Fatal("broadcasting GO did not instantiate the signal-start process")
	}

	// exactly one instance — a single signal-start declaration.
	select {
	case got := <-done:
		t.Fatalf("unexpected extra run %q", got)
	case <-time.After(300 * time.Millisecond):
	}
}

// TestSignalStartBroadcastInstantiatesAll: one broadcast instantiates EVERY
// process declaring a signal StartEvent of that name — broadcast, not
// point-to-point (SRD-026 FR-4).
func TestSignalStartBroadcastInstantiatesAll(t *testing.T) {
	done := make(chan string, 4)
	procA := signalStartProcess(t, "sig-start-a", "GO", "A", done)
	procB := signalStartProcess(t, "sig-start-b", "GO", "B", done)

	th, cancel := runEngine(t, procA)
	defer cancel()
	_, err := th.RegisterProcess(procB)
	require.NoError(t, err)

	require.NoError(t, th.PropagateEvent(context.Background(), sigDef(t, "GO")))

	got := map[string]bool{}

	for range 2 {
		select {
		case m := <-done:
			got[m] = true
		case <-time.After(3 * time.Second):
			t.Fatalf("expected both signal-start processes to instantiate, got %v", got)
		}
	}

	require.True(t, got["A"] && got["B"],
		"one broadcast instantiated both: %v", got)
}

// TestSignalStartEachBroadcastNewInstance: each broadcast instantiates anew — no
// dedup, the starter uses an empty key (SRD-026 FR-4).
func TestSignalStartEachBroadcastNewInstance(t *testing.T) {
	done := make(chan string, 4)
	proc := signalStartProcess(t, "sig-start-multi", "GO", "X", done)

	th, cancel := runEngine(t, proc)
	defer cancel()

	require.NoError(t, th.PropagateEvent(context.Background(), sigDef(t, "GO")))
	require.NoError(t, th.PropagateEvent(context.Background(), sigDef(t, "GO")))

	for i := range 2 {
		select {
		case got := <-done:
			require.Equal(t, "X", got)
		case <-time.After(3 * time.Second):
			t.Fatalf("expected 2 instances from 2 broadcasts, got %d", i)
		}
	}
}
