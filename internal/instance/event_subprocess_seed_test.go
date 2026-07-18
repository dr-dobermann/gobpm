package instance

import (
	"sync/atomic"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/stretchr/testify/require"
)

// sigEventSub builds an interrupting Signal-triggered Event Sub-Process whose
// task bumps ran — a handler that, unarmed (M1), must never run.
func sigEventSub(t *testing.T, name string, ran *atomic.Int32) *activities.SubProcess {
	t.Helper()

	es, err := activities.NewSubProcess(name, activities.WithTriggeredByEvent())
	require.NoError(t, err)

	sig, err := events.NewSignal(name+"-sig",
		data.MustItemDefinition(values.NewVariable(1)))
	require.NoError(t, err)
	start, err := events.NewStartEvent(name+"-start",
		[]options.Option{
			events.WithSignalTrigger(events.MustSignalEventDefinition(sig)),
			events.WithInterrupting(),
		}...)
	require.NoError(t, err)

	task := hitTask(t, name+"-task", ran, "", 0)
	end, err := events.NewEndEvent(name + "-end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, task, end} {
		require.NoError(t, es.Add(e))
	}
	linkAll(t, [2]flow.Element{start, task}, [2]flow.Element{task, end})

	return es
}

// TestEventSubNotSeeded (SRD-052 FR-3): an Event Sub-Process is skipped by
// every entry-seeding path — a top-level one (createTracks) and one inside an
// embedded sub-process's scope (scopeSeeds) — so an unarmed handler never runs
// and the normal flow completes.
func TestEventSubNotSeeded(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var body, topHandler, innerHandler atomic.Int32

	// embedded sub-process: None start → task → end, PLUS an inner event-sub.
	sp, err := activities.NewSubProcess("body")
	require.NoError(t, err)
	sStart, err := events.NewStartEvent("s-start")
	require.NoError(t, err)
	sTask := hitTask(t, "body-task", &body, "", 0)
	sEnd, err := events.NewEndEvent("s-end")
	require.NoError(t, err)
	for _, e := range []flow.Element{sStart, sTask, sEnd, sigEventSub(t, "inner", &innerHandler)} {
		require.NoError(t, sp.Add(e))
	}
	linkAll(t, [2]flow.Element{sStart, sTask}, [2]flow.Element{sTask, sEnd})

	// process: start → body → after → end, PLUS a top-level event-sub.
	after := hitTask(t, "after", &atomic.Int32{}, "", 0)
	p := wrapSP(t, "eventsub-seed", sp, after)
	require.NoError(t, p.Add(sigEventSub(t, "top", &topHandler)))

	inst := runInstance(t, p)
	require.Equal(t, Completed, inst.State())

	require.EqualValues(t, 1, body.Load(), "the body's own flow ran")
	require.Zero(t, topHandler.Load(), "the top-level handler stayed unarmed")
	require.Zero(t, innerHandler.Load(), "the inner handler stayed unarmed")
}
