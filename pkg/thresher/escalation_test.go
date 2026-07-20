package thresher_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

// SRD-058 T-8 — Escalation end-to-end through the public engine surface, with
// the Thrown → Caught / Unresolved observability triple asserted via th.Observe.

// escEED builds an EscalationEventDefinition carrying code (matching is by code).
func escEED(t *testing.T, code string) *events.EscalationEventDefinition {
	t.Helper()

	return events.MustEscalationEventDefinition(
		events.MustEscalation("esc-"+code, code,
			data.MustItemDefinition(values.NewVariable(1))))
}

// escBody builds a sub-process s-start → throw(End, code), the throw raising an
// escalation and ending its token.
func escBody(t *testing.T, code string) *activities.SubProcess {
	t.Helper()

	body, err := activities.NewSubProcess("body")
	require.NoError(t, err)
	sStart, err := events.NewStartEvent("s-start")
	require.NoError(t, err)
	throw, err := events.NewEndEvent("throw",
		events.WithEscalationTrigger(escEED(t, code)))
	require.NoError(t, err)
	for _, e := range []flow.Element{sStart, throw} {
		require.NoError(t, body.Add(e))
	}
	link(t, sStart, throw)

	return body
}

// observeToCompletion runs proc under a fresh engine with an attached collector,
// waits for completion, and returns the drained collector.
func observeToCompletion(t *testing.T, proc *process.Process) *collector {
	t.Helper()

	th, err := thresher.New(proc.Name() + "-engine")
	require.NoError(t, err)

	c := &collector{}
	sub := th.Observe(c)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, th.Run(ctx))
	_, err = th.RegisterProcess(proc)
	require.NoError(t, err)

	h, err := th.StartLatest(proc.ID())
	require.NoError(t, err)

	wctx, wcancel := context.WithTimeout(ctx, 5*time.Second)
	defer wcancel()
	state, werr := h.WaitCompletion(wctx)
	require.NoError(t, werr)
	require.Equal(t, thresher.StateCompleted, state,
		"a caught/unresolved escalation is non-critical — the instance completes")

	require.NoError(t, th.Shutdown(context.Background()))
	sub.Cancel() // drains buffered facts

	return c
}

// TestEscalationBoundaryE2E (T-8): an Escalation End Event inside a sub-process
// is caught by an interrupting Escalation boundary; end to end the instance
// completes and the observer sees the Thrown → Caught pair.
func TestEscalationBoundaryE2E(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	body := escBody(t, "E")

	proc, err := process.New("esc-e2e")
	require.NoError(t, err)
	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	normalEnd, err := events.NewEndEvent("normal-end")
	require.NoError(t, err)
	be, err := events.NewBoundaryEvent("esc-bnd", body, escEED(t, "E"), true)
	require.NoError(t, err)
	var handled atomic.Bool
	handle := laneTask(t, "handle", &handled)
	excEnd, err := events.NewEndEvent("exc-end")
	require.NoError(t, err)
	for _, e := range []flow.Element{start, body, normalEnd, be, handle, excEnd} {
		require.NoError(t, proc.Add(e))
	}
	link(t, start, body)
	link(t, body, normalEnd)
	link(t, be, handle)
	link(t, handle, excEnd)

	c := observeToCompletion(t, proc)

	require.True(t, handled.Load(), "the exception flow ran")
	require.True(t,
		c.sawKindPhase(observability.KindEscalation, observability.PhaseThrown),
		"the throw emitted Thrown")
	require.True(t,
		c.sawKindPhase(observability.KindEscalation, observability.PhaseCaught),
		"the boundary emitted Caught")
}

// TestEscalationUnresolvedE2E (T-8): an escalation with no catcher completes the
// instance (non-fault) and the observer sees Thrown → Unresolved.
func TestEscalationUnresolvedE2E(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	body := escBody(t, "NOPE")

	proc, err := process.New("esc-unresolved-e2e")
	require.NoError(t, err)
	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)
	for _, e := range []flow.Element{start, body, end} {
		require.NoError(t, proc.Add(e))
	}
	link(t, start, body)
	link(t, body, end)

	c := observeToCompletion(t, proc)

	require.True(t,
		c.sawKindPhase(observability.KindEscalation, observability.PhaseThrown),
		"the throw emitted Thrown")
	require.True(t,
		c.sawKindPhase(observability.KindEscalation, observability.PhaseUnresolved),
		"the unresolved escalation was logged, not silently dropped")
}
