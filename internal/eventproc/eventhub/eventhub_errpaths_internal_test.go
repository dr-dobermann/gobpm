package eventhub

import (
	"context"
	"errors"
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/renv"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestPropagateEventProcessError covers EventHub.PropagateEvent's
// waiter-failure branch: a non-signal event with a registered waiter whose
// Process fails yields a wrapped "processing failed" error.
func TestPropagateEventProcessError(t *testing.T) {
	hub, err := New(enginert.Default())
	require.NoError(t, err)
	require.NoError(t, hub.Start(context.Background()))

	// a non-signal definition (terminate) is routed to eh.waiters[id].Process
	def, err := events.NewTerminateEventDefinition()
	require.NoError(t, err)

	w := mockeventproc.NewMockEventWaiter(t)
	w.EXPECT().Process(mock.Anything).Return(errors.New("process boom"))
	w.EXPECT().ID().Return("mock-waiter")

	hub.m.Lock()
	hub.waiters[def.ID()] = w
	hub.m.Unlock()

	require.Error(t, hub.PropagateEvent(context.Background(), def))
}

// TestRegisterEventAddProcessorError covers EventHub.RegisterEvent's
// add-processor failure branch: when a waiter already exists for the event and
// AddEventProcessor fails, the error is wrapped and returned.
func TestRegisterEventAddProcessorError(t *testing.T) {
	hub, err := New(enginert.Default())
	require.NoError(t, err)
	require.NoError(t, hub.Start(context.Background()))

	def, err := events.NewTerminateEventDefinition()
	require.NoError(t, err)

	ep := mockeventproc.NewMockEventProcessor(t)
	ep.EXPECT().ID().Return("ep-1").Maybe()

	w := mockeventproc.NewMockEventWaiter(t)
	w.EXPECT().AddEventProcessor(ep).Return(errors.New("add boom"))
	w.EXPECT().ID().Return("w-1").Maybe()

	hub.m.Lock()
	hub.waiters[def.ID()] = w
	hub.m.Unlock()

	require.Error(t, hub.RegisterEvent(ep, def))
}

// TestSignalCatchersFallbackCount (FIX-021): a signalIdx entry that does not
// expose ProcessorCount (not the concrete signalWaiter) still counts as one
// catcher — the defensive fallback of the readiness probe.
func TestSignalCatchersFallbackCount(t *testing.T) {
	eh, err := New(enginert.Default())
	require.NoError(t, err)

	// a bare mock EventWaiter has no ProcessorCount → the fallback branch.
	eh.signalIdx["GO"] = []eventproc.EventWaiter{
		mockeventproc.NewMockEventWaiter(t),
	}

	require.Equal(t, 1, eh.SignalCatchers("GO"))
}

// TestBroadcastSignalProcessError covers broadcastSignal's defensive branch
// (FIX-022 A1): a signalIdx waiter whose Process returns an error is logged and
// the broadcast continues (best-effort — it must reach every catcher, FIX-007),
// so broadcastSignal itself still returns nil.
func TestBroadcastSignalProcessError(t *testing.T) {
	hub, err := New(enginert.Default())
	require.NoError(t, err)
	require.NoError(t, hub.Start(context.Background()))

	sig, err := events.NewSignal("GO", nil)
	require.NoError(t, err)
	def, err := events.NewSignalEventDefinition(sig)
	require.NoError(t, err)

	w := mockeventproc.NewMockEventWaiter(t)
	w.EXPECT().Process(mock.Anything).Return(errors.New("process boom"))
	w.EXPECT().ID().Return("mock-signal-waiter")

	hub.m.Lock()
	hub.signalIdx["GO"] = []eventproc.EventWaiter{w}
	hub.m.Unlock()

	require.NoError(t, hub.broadcastSignal(def),
		"a per-waiter Process error is logged, not propagated")
}

// The five branches below construct classified errors carrying vocabulary
// details, and every one was uncovered before FIX-035 — the diagnostic detail a
// user would see when registration or teardown fails was itself never
// exercised. Reading them to write these tests found two real defects the
// mechanical sweep could not: errs.D("event_definition_idf", …), a typo naming
// a key nobody could ever grep for, and errs.D("event_waiter_id", …), a synonym
// of the canonical waiter_id that ADR-022 v.2 §2.5's one-entity-one-key rule
// forbids. Both are fixed; neither matched a canonical value, so only reading
// the code could catch them.

// TestRegisterWaiterRejectedWhenHubStopped: a shut-down hub refuses
// registration, naming the event definition it refused (eventhub.go §registerWaiter).
func TestRegisterWaiterRejectedWhenHubStopped(t *testing.T) {
	hub, err := New(enginert.Default())
	require.NoError(t, err)

	def, err := events.NewTerminateEventDefinition()
	require.NoError(t, err)

	ep := mockeventproc.NewMockEventProcessor(t)
	ep.EXPECT().ID().Return("ep-stopped").Maybe()

	hub.setState(hubStopped)

	err = hub.registerWaiter(ep, def,
		func(eventproc.EventHub, eventproc.EventProcessor,
			flow.EventDefinition, renv.EngineRuntime,
		) (eventproc.EventWaiter, error) {
			t.Fatal("builder must not run once the hub is stopped")

			return nil, nil
		})

	require.ErrorContains(t, err, "shut down")
}

// TestRegisterWaiterBuildFailure: when the waiter builder fails, the error
// names both the processor and the definition, so an operator can tell WHICH
// registration failed rather than only that one did.
func TestRegisterWaiterBuildFailure(t *testing.T) {
	hub, err := New(enginert.Default())
	require.NoError(t, err)
	require.NoError(t, hub.Start(context.Background()))

	def, err := events.NewTerminateEventDefinition()
	require.NoError(t, err)

	ep := mockeventproc.NewMockEventProcessor(t)
	ep.EXPECT().ID().Return("ep-build").Maybe()

	err = hub.registerWaiter(ep, def,
		func(eventproc.EventHub, eventproc.EventProcessor,
			flow.EventDefinition, renv.EngineRuntime,
		) (eventproc.EventWaiter, error) {
			return nil, errors.New("build boom")
		})

	require.ErrorContains(t, err, "building failed")
}

// TestUnregisterEventRemoveProcessorFailure: a waiter that refuses to drop its
// processor fails the unregistration, naming waiter, processor and definition.
func TestUnregisterEventRemoveProcessorFailure(t *testing.T) {
	hub, err := New(enginert.Default())
	require.NoError(t, err)
	require.NoError(t, hub.Start(context.Background()))

	def, err := events.NewTerminateEventDefinition()
	require.NoError(t, err)

	ep := mockeventproc.NewMockEventProcessor(t)
	ep.EXPECT().ID().Return("ep-rm").Maybe()

	w := mockeventproc.NewMockEventWaiter(t)
	w.EXPECT().RemoveEventProcessor(ep).Return(errors.New("remove boom"))
	w.EXPECT().ID().Return("w-rm").Maybe()

	hub.m.Lock()
	hub.waiters[def.ID()] = w
	hub.m.Unlock()

	require.ErrorContains(t, hub.UnregisterEvent(ep, def.ID()),
		"couldn't remove event processor")
}

// TestUnregisterEventStopFailure: dropping the LAST processor stops the waiter,
// and a failing Stop is reported rather than swallowed — the waiter would
// otherwise stay running with nobody listening.
func TestUnregisterEventStopFailure(t *testing.T) {
	hub, err := New(enginert.Default())
	require.NoError(t, err)
	require.NoError(t, hub.Start(context.Background()))

	def, err := events.NewTerminateEventDefinition()
	require.NoError(t, err)

	ep := mockeventproc.NewMockEventProcessor(t)
	ep.EXPECT().ID().Return("ep-stop").Maybe()

	w := mockeventproc.NewMockEventWaiter(t)
	w.EXPECT().RemoveEventProcessor(ep).Return(nil)
	w.EXPECT().EventProcessors().Return(nil)
	w.EXPECT().State().Return(eventproc.WSRunned)
	w.EXPECT().Stop().Return(errors.New("stop boom"))
	w.EXPECT().ID().Return("w-stop").Maybe()
	w.EXPECT().EventDefinition().Return(def).Maybe()

	hub.m.Lock()
	hub.waiters[def.ID()] = w
	hub.m.Unlock()

	require.ErrorContains(t, hub.UnregisterEvent(ep, def.ID()), "waiter stop failed")
}

// TestRemoveWaiterNotFound: removing a waiter that was never registered names
// the definition it looked for.
func TestRemoveWaiterNotFound(t *testing.T) {
	hub, err := New(enginert.Default())
	require.NoError(t, err)

	require.ErrorContains(t, hub.RemoveWaiter("no-such-def"), "waiter isn't found")
}
