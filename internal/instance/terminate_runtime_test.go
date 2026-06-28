package instance

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
)

// SRD-030 M2 — the Terminate End Event behaviour. Reaching a Terminate End Event
// abnormally terminates the instance (every track cancelled, tokens discarded,
// state Terminated), it is not a fault, and it travels the loop's native evTerminate
// lane (deterministic by FIFO, no select race).

// terminateEnd builds an End Event carrying a Terminate trigger.
func terminateEnd(t *testing.T, id string) *events.EndEvent {
	t.Helper()

	termEd, err := events.NewTerminateEventDefinition()
	require.NoError(t, err)

	ee, err := events.NewEndEvent(id, events.WithTerminateTrigger(termEd))
	require.NoError(t, err)

	return ee
}

// runSingleTerminate builds and runs `start -> terminate-end` and returns the
// finished instance.
func runSingleTerminate(t *testing.T) *Instance {
	t.Helper()

	_ = data.CreateDefaultStates()

	p, err := process.New("srd030-single")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	end := terminateEnd(t, "term-end")

	for _, e := range []flow.Element{start, end} {
		require.NoError(t, p.Add(e))
	}

	_, err = flow.Link(start, end)
	require.NoError(t, err)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(), &recordingProducer{}, nil)
	require.NoError(t, err)

	runToDone(t, inst)

	return inst
}

// TestTrackEventKindStringTerminate covers the new kind name and the out-of-range
// "unknown" branch of the data-driven String() table.
func TestTrackEventKindStringTerminate(t *testing.T) {
	require.Equal(t, "terminate", evTerminate.String())
	require.Equal(t, "unknown", trackEventKind(200).String())
}

// T-1: a single-track `start -> terminate-end` settles Terminated, not Completed.
func TestTerminateEndEventTerminatesInstance(t *testing.T) {
	inst := runSingleTerminate(t)

	// start -> terminate-end has only one terminal path, so Terminated (not Completed)
	// is itself proof the Terminate End Event ran.
	require.Equal(t, Terminated, inst.State(),
		"a Terminate End Event abnormally terminates the instance")
}

// T-6: Terminate is NOT a fault — the instance reaches Terminated with no recorded
// error (distinct from an Error End Event, which faults with a BpmnError, SRD-029 T-11).
func TestTerminateIsNotAFault(t *testing.T) {
	inst := runSingleTerminate(t)

	require.Equal(t, Terminated, inst.State())
	require.NoError(t, inst.LastErr(), "Terminate is a clean abnormal end, not a fault")
}

// T-3: with Terminate as the last (here: only) active track, the terminal state is
// deterministic — evTerminate is FIFO-before the track's own evEnded, so stopping is
// set first. Run many times under -race; without that ordering it would flake to
// Completed.
func TestTerminateLastTrackDeterministic(t *testing.T) {
	for i := 0; i < 50; i++ {
		inst := runSingleTerminate(t)
		require.Equal(t, Terminated, inst.State(), "iteration %d settled non-Terminated", i)
	}
}

// T-4: a process ending at a plain (None) End Event completes normally — no
// regression from the Terminate branch (NFR-2).
func TestNoneEndEventCompletes(t *testing.T) {
	_ = data.CreateDefaultStates()

	p, err := process.New("srd030-none")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	end, err := events.NewEndEvent("none-end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, end} {
		require.NoError(t, p.Add(e))
	}

	_, err = flow.Link(start, end)
	require.NoError(t, err)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(), &recordingProducer{}, nil)
	require.NoError(t, err)

	runToDone(t, inst)

	require.Equal(t, Completed, inst.State())
	require.NoError(t, inst.LastErr())
}

// blockingOp returns an operation that blocks until its context is cancelled — it
// only ends when the instance terminates and cancels its track.
func blockingOp(t *testing.T) service.Operation {
	t.Helper()

	op, err := gooper.New("block",
		func(ctx context.Context, _ service.DataReader,
			_ *data.ItemDefinition,
		) (*data.ItemDefinition, error) {
			<-ctx.Done()

			return nil, ctx.Err()
		})
	require.NoError(t, err)

	return op
}

// splitStart builds `start -> parallelSplit` into p and returns the split gateway for
// the caller to wire its two branches onto.
func splitStart(t *testing.T, p *process.Process) *gateways.ParallelGateway {
	t.Helper()

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	split, err := gateways.NewParallelGateway(gateways.WithDirection(gateways.Diverging))
	require.NoError(t, err)

	require.NoError(t, p.Add(start))
	require.NoError(t, p.Add(split))

	_, err = flow.Link(start, split)
	require.NoError(t, err)

	return split
}

// instOf snapshots p and builds a fresh instance over it.
func instOf(t *testing.T, p *process.Process) *Instance {
	t.Helper()

	s, err := snapshot.New(p)
	require.NoError(t, err)

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(), &recordingProducer{}, nil)
	require.NoError(t, err)

	return inst
}

// T-2: one branch reaches a Terminate End Event while a sibling is blocked in a
// running activity — the sibling's context is cancelled (its downstream node is never
// reached) and the instance terminates.
func TestTerminateCancelsRunningSibling(t *testing.T) {
	_ = data.CreateDefaultStates()

	p, err := process.New("srd030-sibling")
	require.NoError(t, err)

	split := splitStart(t, p)

	termEnd := terminateEnd(t, "term-end")

	slow, err := activities.NewServiceTask("slow", blockingOp(t), activities.WithoutParams())
	require.NoError(t, err)

	siblingEnd, err := events.NewEndEvent("sibling-end")
	require.NoError(t, err)

	for _, e := range []flow.Element{termEnd, slow, siblingEnd} {
		require.NoError(t, p.Add(e))
	}

	_, err = flow.Link(split, termEnd) // branch A: -> terminate-end
	require.NoError(t, err)
	_, err = flow.Link(split, slow) // branch B: -> slow -> sibling-end
	require.NoError(t, err)
	_, err = flow.Link(slow, siblingEnd)
	require.NoError(t, err)

	inst := instOf(t, p)

	runToDone(t, inst)

	require.Equal(t, Terminated, inst.State())
	require.False(t, reachedNode(inst, "sibling-end"),
		"the cancelled sibling did not run its downstream node")
}

// T-7: two concurrent branches each reach a Terminate End Event — both fire, the
// repeated evTerminate is idempotent (stopAll's stopping guard), the instance
// terminates without panic.
func TestTwoTerminateBranchesIdempotent(t *testing.T) {
	_ = data.CreateDefaultStates()

	p, err := process.New("srd030-two-term")
	require.NoError(t, err)

	split := splitStart(t, p)

	termA := terminateEnd(t, "term-a")
	termB := terminateEnd(t, "term-b")

	require.NoError(t, p.Add(termA))
	require.NoError(t, p.Add(termB))

	_, err = flow.Link(split, termA)
	require.NoError(t, err)
	_, err = flow.Link(split, termB)
	require.NoError(t, err)

	inst := instOf(t, p)

	runToDone(t, inst)

	require.Equal(t, Terminated, inst.State())
	require.NoError(t, inst.LastErr())
}
