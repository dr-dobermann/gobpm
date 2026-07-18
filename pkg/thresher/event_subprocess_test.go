package thresher_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/stretchr/testify/require"
)

// SRD-052 e2e — the interrupting Event Sub-Process through the public engine
// surface: a handler armed while its enclosing scope is open fires, cancels the
// scope's blocked work, runs its own flow, and lets the host resume.

// neverReceive builds a ReceiveTask that parks forever (its message is never
// sent) — the live sibling an interrupting handler must cancel.
func neverReceive(t *testing.T, name string) *activities.ReceiveTask {
	t.Helper()

	rt, err := activities.NewReceiveTask(name,
		bpmncommon.MustMessage("never-"+name,
			data.MustItemDefinition(values.NewVariable(1))))
	require.NoError(t, err)

	return rt
}

// shortTimer builds a Timer definition that fires ~120ms after it arms — long
// enough for the scope to open and block, short enough to keep the test fast.
func shortTimer(t *testing.T) *events.TimerEventDefinition {
	t.Helper()

	return events.MustTimerEventDefinition(
		goexpr.Must(nil,
			data.MustItemDefinition(
				values.NewVariable(time.Now().Add(120*time.Millisecond))),
			func(_ context.Context, _ data.Source) (data.Value, error) {
				return values.NewVariable(
					time.Now().Add(120 * time.Millisecond)), nil
			}),
		nil, nil)
}

// TestEventSubProcessE2E (SRD-052 §6): a Timer-triggered interrupting Event
// Sub-Process inside a sub-process fires, cancels the scope's blocked receive,
// runs its handler, and — reaching its End without re-throwing — absorbs the
// event so the host resumes and the instance completes.
func TestEventSubProcessE2E(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var ran atomic.Bool

	// event-sub handler: timer-start → mark → end.
	es, err := activities.NewSubProcess("on-timeout",
		activities.WithTriggeredByEvent())
	require.NoError(t, err)
	tStart, err := events.NewStartEvent("t-start",
		events.WithTimerTrigger(shortTimer(t)))
	require.NoError(t, err)
	mark := laneTask(t, "handle-timeout", &ran)
	tEnd, err := events.NewEndEvent("t-end")
	require.NoError(t, err)
	for _, e := range []flow.Element{tStart, mark, tEnd} {
		require.NoError(t, es.Add(e))
	}
	link(t, tStart, mark)
	link(t, mark, tEnd)

	// sub-process body: s-start → stuck (blocks) → s-end, PLUS the handler.
	body, err := activities.NewSubProcess("body")
	require.NoError(t, err)
	sStart, err := events.NewStartEvent("s-start")
	require.NoError(t, err)
	stuck := neverReceive(t, "stuck")
	sEnd, err := events.NewEndEvent("s-end")
	require.NoError(t, err)
	for _, e := range []flow.Element{sStart, stuck, sEnd, es} {
		require.NoError(t, body.Add(e))
	}
	link(t, sStart, stuck)
	link(t, stuck, sEnd)

	proc := wrapBody(t, "event-sub-timer-e2e", body)

	require.NoError(t, runFlows(t, proc))
	require.True(t, ran.Load(),
		"the timer handler must run after cancelling the blocked scope")
}

// TestEventSubConditionalStartE2E (SRD-052 §6): a Conditional-start interrupting
// Event Sub-Process — the conditional START (ADR-006 v.3's deferred piece) —
// fires on a sibling task's committed data (false→true), cancels the scope, and
// runs its handler.
func TestEventSubConditionalStartE2E(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var ran atomic.Bool

	// event-sub handler: conditional-start (flag>0) → mark → end.
	es, err := activities.NewSubProcess("on-flag",
		activities.WithTriggeredByEvent())
	require.NoError(t, err)
	cStart, err := events.NewStartEvent("c-start",
		events.WithConditionalTrigger(
			events.MustConditionalEventDefinition(watchGt(t, "flag", 0))))
	require.NoError(t, err)
	mark := laneTask(t, "handle-flag", &ran)
	cEnd, err := events.NewEndEvent("c-end")
	require.NoError(t, err)
	for _, e := range []flow.Element{cStart, mark, cEnd} {
		require.NoError(t, es.Add(e))
	}
	link(t, cStart, mark)
	link(t, mark, cEnd)

	// body: s-start → raise(flag=1) → stuck → s-end, PLUS the handler. The raise
	// commit flips the condition; the handler then cancels the still-blocked
	// stuck. The flag property lives at the process root so the handler's
	// condition finds it by walk-up.
	body, err := activities.NewSubProcess("body")
	require.NoError(t, err)
	sStart, err := events.NewStartEvent("s-start")
	require.NoError(t, err)
	raise := commitTask(t, "raise", "flag", 1)
	stuck := neverReceive(t, "stuck")
	sEnd, err := events.NewEndEvent("s-end")
	require.NoError(t, err)
	for _, e := range []flow.Element{sStart, raise, stuck, sEnd, es} {
		require.NoError(t, body.Add(e))
	}
	link(t, sStart, raise)
	link(t, raise, stuck)
	link(t, stuck, sEnd)

	proc := wrapBody(t, "event-sub-cond-e2e", body,
		data.WithProperties(
			data.MustProperty("flag",
				data.MustItemDefinition(values.NewVariable(0),
					foundation.WithID("flag")),
				data.ReadyDataState)))

	require.NoError(t, runFlows(t, proc))
	require.True(t, ran.Load(),
		"the conditional handler must run on the flag commit")
}

// TestNonInterruptingEventSubProcessE2E (SRD-053): a non-interrupting
// Conditional handler fires on a sibling task's committed data and runs
// CONCURRENTLY with the scope's normal work — the scope is NOT cancelled, so the
// main flow completes too, and the parent resumes when both drain.
func TestNonInterruptingEventSubProcessE2E(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var charged, handled atomic.Bool

	// non-interrupting conditional handler: c-start(flag>0) → handle → end.
	es, err := activities.NewSubProcess("on-flag",
		activities.WithTriggeredByEvent())
	require.NoError(t, err)
	cStart, err := events.NewStartEvent("c-start",
		events.WithConditionalTrigger(
			events.MustConditionalEventDefinition(watchGt(t, "flag", 0))),
		events.WithNonInterrupting())
	require.NoError(t, err)
	handle := laneTask(t, "handle-flag", &handled)
	cEnd, err := events.NewEndEvent("c-end")
	require.NoError(t, err)
	for _, e := range []flow.Element{cStart, handle, cEnd} {
		require.NoError(t, es.Add(e))
	}
	link(t, cStart, handle)
	link(t, handle, cEnd)

	// body: s-start → raise(flag=1) → charge → s-end, PLUS the handler. The raise
	// commit flips the condition; the handler runs while charge continues (the
	// scope is not cancelled), and both must complete.
	body, err := activities.NewSubProcess("body")
	require.NoError(t, err)
	sStart, err := events.NewStartEvent("s-start")
	require.NoError(t, err)
	raise := commitTask(t, "raise", "flag", 1)
	charge := laneTask(t, "charge", &charged)
	sEnd, err := events.NewEndEvent("s-end")
	require.NoError(t, err)
	for _, e := range []flow.Element{sStart, raise, charge, sEnd, es} {
		require.NoError(t, body.Add(e))
	}
	link(t, sStart, raise)
	link(t, raise, charge)
	link(t, charge, sEnd)

	proc := wrapBody(t, "ni-eventsub-e2e", body,
		data.WithProperties(
			data.MustProperty("flag",
				data.MustItemDefinition(values.NewVariable(0),
					foundation.WithID("flag")),
				data.ReadyDataState)))

	require.NoError(t, runFlows(t, proc))
	require.True(t, charged.Load(),
		"the main flow completed — a non-interrupting fire does not cancel it")
	require.True(t, handled.Load(),
		"the non-interrupting handler ran concurrently on the flag commit")
}

// wrapBody builds a process start → body → end around a sub-process, carrying
// any process-level options (e.g. root properties the handler reads by walk-up).
func wrapBody(
	t *testing.T, name string, body *activities.SubProcess,
	opts ...options.Option,
) *process.Process {
	t.Helper()

	proc, err := process.New(name, opts...)
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

	return proc
}
