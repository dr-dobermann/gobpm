package eventhub

import (
	"bytes"
	"context"
	"errors"
	"log/slog"

	"sync"
	"sync/atomic"
	"testing"
	"time"

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

// TestRegisterWaiterDoesNotHoldTheLockAcrossService is FIX-038 T-1.
//
// Building and starting a waiter is FOREIGN work: a message waiter's Service
// subscribes to the host's broker, which may be remote and may block. The hub
// used to hold its ONE lock across that call, so while a broker was slow no
// waiter could be registered, unregistered or looked up anywhere in the engine.
//
// The interleaving is driven directly: Service blocks until the test releases
// it, and a concurrent hub operation must complete meanwhile.
func TestRegisterWaiterDoesNotHoldTheLockAcrossService(t *testing.T) {
	hub, err := New(enginert.Default())
	require.NoError(t, err)
	require.NoError(t, hub.Start(context.Background()))

	def, err := events.NewTerminateEventDefinition()
	require.NoError(t, err)

	ep := mockeventproc.NewMockEventProcessor(t)
	ep.EXPECT().ID().Return("ep-slow").Maybe()

	inService := make(chan struct{})
	release := make(chan struct{})

	w := mockeventproc.NewMockEventWaiter(t)
	w.EXPECT().ID().Return("w-slow").Maybe()
	w.EXPECT().EventDefinition().Return(def).Maybe()
	w.EXPECT().Service(mock.Anything).RunAndReturn(
		func(context.Context) error {
			close(inService)
			<-release

			return nil
		}).Once()

	registered := make(chan error, 1)

	go func() {
		registered <- hub.registerWaiter(ep, def,
			func(eventproc.EventHub, eventproc.EventProcessor,
				flow.EventDefinition, renv.EngineRuntime,
			) (eventproc.EventWaiter, error) {
				return w, nil
			})
	}()

	<-inService // the waiter is mid-Service, inside the registration

	// The hub must still be usable. Under the old code this blocked until the
	// broker returned, which is the whole defect.
	done := make(chan struct{})

	go func() {
		defer close(done)

		_ = hub.RemoveWaiter("nothing-here") // any operation needing eh.m
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the hub lock was held across the waiter's Service call")
	}

	close(release)
	require.NoError(t, <-registered)
}

// countingWaiter is a minimal, concurrency-safe EventWaiter for the race
// traces: a mockery mock inside a hot loop asserts call counts this test does
// not care about, and hides the state it does.
type countingWaiter struct {
	def flow.EventDefinition

	mu      sync.Mutex
	procs   []eventproc.EventProcessor
	state   eventproc.EventWaiterState
	stopped chan struct{}
}

func newCountingWaiter(def flow.EventDefinition) *countingWaiter {
	return &countingWaiter{def: def, stopped: make(chan struct{})}
}

func (w *countingWaiter) ID() string                            { return "counting-" + w.def.ID() }
func (w *countingWaiter) EventDefinition() flow.EventDefinition { return w.def }
func (w *countingWaiter) Process(flow.EventDefinition) error    { return nil }
func (w *countingWaiter) Done() <-chan struct{}                 { return w.stopped }

func (w *countingWaiter) AddEventProcessor(ep eventproc.EventProcessor) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.procs = append(w.procs, ep)

	return nil
}

func (w *countingWaiter) RemoveEventProcessor(eventproc.EventProcessor) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if len(w.procs) > 0 {
		w.procs = w.procs[:len(w.procs)-1]
	}

	return nil
}

func (w *countingWaiter) EventProcessors() []eventproc.EventProcessor {
	w.mu.Lock()
	defer w.mu.Unlock()

	return append([]eventproc.EventProcessor{}, w.procs...)
}

func (w *countingWaiter) Service(context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.state = eventproc.WSRunned

	return nil
}

func (w *countingWaiter) Stop() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.state == eventproc.WSRunned {
		w.state = eventproc.WSEnded
		close(w.stopped)
	}

	return nil
}

func (w *countingWaiter) State() eventproc.EventWaiterState {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.state
}

// TestUnregisterRacingRegisterKeepsTheRegistration is FIX-038 T-3.
//
// UnregisterEvent read the waiter under RLock, RELEASED, then removed the
// processor, tested emptiness, stopped the waiter and unmapped it. A
// registerWaiter landing in that window found the waiter still mapped and
// attached a processor to it — and this call then stopped and unmapped it. The
// registration reported success and its events never arrived.
//
// The invariant: a registration that returns nil leaves its definition MAPPED.
// Hammering both paths concurrently under -race exercises the window the fix
// closes; the assertion is the invariant, not a timing.
func TestUnregisterRacingRegisterKeepsTheRegistration(t *testing.T) {
	hub, err := New(enginert.Default())
	require.NoError(t, err)
	require.NoError(t, hub.Start(context.Background()))

	def, err := events.NewTerminateEventDefinition()
	require.NoError(t, err)

	// The real builders receive ep and register it inside the waiter they
	// return (waiters.CreateWaiter), so the stub must too — a waiter installed
	// without its processor is not the state the hub ever produces.
	build := func(_ eventproc.EventHub, ep eventproc.EventProcessor,
		_ flow.EventDefinition, _ renv.EngineRuntime,
	) (eventproc.EventWaiter, error) {
		w := newCountingWaiter(def)
		_ = w.AddEventProcessor(ep)

		return w, nil
	}

	for range 200 {
		ep := mockeventproc.NewMockEventProcessor(t)
		ep.EXPECT().ID().Return("ep-race").Maybe()

		var wg sync.WaitGroup

		wg.Add(2)

		regErr := make(chan error, 1)

		go func() {
			defer wg.Done()

			regErr <- hub.registerWaiter(ep, def, build)
		}()

		go func() {
			defer wg.Done()

			_ = hub.UnregisterEvent(ep, def.ID())
		}()

		wg.Wait()

		if err := <-regErr; err != nil {
			continue // a refused registration promises nothing
		}

		// It reported success, so the definition must be served.
		hub.m.Lock()
		w, mapped := hub.waiters[def.ID()]
		hub.m.Unlock()

		if !mapped {
			continue // the unregister ran last and legitimately removed it
		}

		require.NotEmpty(t, w.EventProcessors(),
			"a mapped waiter must carry the processors registered against it")

		_ = hub.UnregisterEvent(ep, def.ID()) // reset for the next round
	}
}

// faultyWaiter is a countingWaiter whose two failure points can be armed: the
// join a losing registration performs on the winner, and the teardown of a
// waiter that was built but never installed.
type faultyWaiter struct {
	*countingWaiter

	// id distinguishes two waiters for ONE definition — countingWaiter derives
	// its id from the definition, so a winner and a loser are otherwise
	// indistinguishable in a fact.
	id      string
	addErr  error
	stopErr error
	stops   atomic.Int32
}

func (w *faultyWaiter) ID() string {
	if w.id != "" {
		return w.id
	}

	return w.countingWaiter.ID()
}

func (w *faultyWaiter) AddEventProcessor(ep eventproc.EventProcessor) error {
	if w.addErr != nil {
		return w.addErr
	}

	return w.countingWaiter.AddEventProcessor(ep)
}

func (w *faultyWaiter) Stop() error {
	w.stops.Add(1)

	_ = w.countingWaiter.Stop()

	return w.stopErr
}

// TestRegisterOnAStoppedHubIsRejectedAndTearsDown covers the branch the §1.3
// restructure introduced: the builder runs BEFORE the lock, so a registration
// that arrives at a stopped hub is holding a live waiter nothing will install.
// It must be rejected AND torn down — a waiter left running past Shutdown is
// exactly what SRD-019 forbids.
func TestRegisterOnAStoppedHubIsRejectedAndTearsDown(t *testing.T) {
	hub, err := New(enginert.Default())
	require.NoError(t, err)
	require.NoError(t, hub.Start(context.Background()))

	def, err := events.NewTerminateEventDefinition()
	require.NoError(t, err)

	// Stop fails too: an uninstalled waiter's teardown failure is a Debug fact,
	// not a second error for the caller, who already has the real one.
	built := &faultyWaiter{
		countingWaiter: newCountingWaiter(def),
		stopErr:        errors.New("the waiter will not stop"),
	}

	// The hub shuts down DURING the unlocked build. An already-stopped hub is
	// refused at the fast path; this window — open only because building no
	// longer holds the lock (§1.1) — is what leaves a live waiter in the hands
	// of a registration that has nowhere to install it.
	build := func(_ eventproc.EventHub, ep eventproc.EventProcessor,
		_ flow.EventDefinition, _ renv.EngineRuntime,
	) (eventproc.EventWaiter, error) {
		require.NoError(t, hub.Shutdown(context.Background()))

		_ = built.AddEventProcessor(ep)

		return built, nil
	}

	ep := mockeventproc.NewMockEventProcessor(t)
	ep.EXPECT().ID().Return("ep-stopped").Maybe()

	err = hub.registerWaiter(ep, def, build)
	require.Error(t, err, "a stopped hub rejects the registration")
	require.ErrorContains(t, err, "shut down")

	require.Positive(t, built.stops.Load(),
		"the uninstalled waiter must be torn down, not left running")
}

// TestRegisterJoinFailureIsReported covers the other uninstalled-waiter branch:
// the registration lost the race to install, so it joins the winner instead —
// and the join can fail. The caller must be told, because its processor is
// subscribed to nothing at all: it is neither the winner's nor its own.
func TestRegisterJoinFailureIsReported(t *testing.T) {
	hub, err := New(enginert.Default())
	require.NoError(t, err)
	require.NoError(t, hub.Start(context.Background()))

	def, err := events.NewTerminateEventDefinition()
	require.NoError(t, err)

	// a winner that refuses joiners, installed DURING the unlocked build — the
	// window the §1.1 restructure opens, and the only way the losing branch is
	// reached: the fast path checked the map before this waiter existed.
	winner := &faultyWaiter{
		countingWaiter: newCountingWaiter(def),
		addErr:         errors.New("the winner refuses the processor"),
	}

	built := &faultyWaiter{countingWaiter: newCountingWaiter(def)}

	build := func(_ eventproc.EventHub, ep eventproc.EventProcessor,
		_ flow.EventDefinition, _ renv.EngineRuntime,
	) (eventproc.EventWaiter, error) {
		hub.m.Lock()
		hub.waiters[def.ID()] = winner
		hub.m.Unlock()

		_ = built.AddEventProcessor(ep)

		return built, nil
	}

	ep := mockeventproc.NewMockEventProcessor(t)
	ep.EXPECT().ID().Return("ep-join-fails").Maybe()

	err = hub.registerWaiter(ep, def, build)
	require.Error(t, err, "a failed join is not a successful registration")
	require.ErrorContains(t, err, "couldn't add event processor to waiter")

	require.Positive(t, built.stops.Load(),
		"the losing registration's own waiter must be torn down")
}

// TestRemoveWaiterDropsTheRegistration covers RemoveWaiter's success path: the
// waiter leaves the registry and the name index together, through the same
// dropWaiterLocked the §1.3 restructure made the single removal point. Removing
// it twice reports not-found rather than silently succeeding.
func TestRemoveWaiterDropsTheRegistration(t *testing.T) {
	hub, err := New(enginert.Default())
	require.NoError(t, err)
	require.NoError(t, hub.Start(context.Background()))

	def, err := events.NewTerminateEventDefinition()
	require.NoError(t, err)

	w := newCountingWaiter(def)

	hub.m.Lock()
	hub.waiters[def.ID()] = w
	hub.m.Unlock()

	require.NoError(t, hub.RemoveWaiter(def.ID()))

	hub.m.Lock()
	_, present := hub.waiters[def.ID()]
	hub.m.Unlock()

	require.False(t, present, "the waiter is gone from the registry")

	err = hub.RemoveWaiter(def.ID())
	require.Error(t, err, "removing it again is not a silent success")
	require.ErrorContains(t, err, "waiter isn't found")
}

// TestLostRegistrationReportsTheServingWaiter is the independent review's
// finding A4. On the losing path the processor is attached to the WINNER, but
// the PhaseRegistered fact named `w` — the waiter this call built, stopped and
// discarded a moment earlier. An operator following that id lands on a waiter
// that no longer exists.
func TestLostRegistrationReportsTheServingWaiter(t *testing.T) {
	var buf bytes.Buffer

	logger := slog.New(slog.NewTextHandler(&buf,
		&slog.HandlerOptions{Level: slog.LevelDebug}))

	hub, err := New(enginert.Default().WithLogger(logger))
	require.NoError(t, err)
	require.NoError(t, hub.Start(context.Background()))

	def, err := events.NewTerminateEventDefinition()
	require.NoError(t, err)

	winner := &faultyWaiter{countingWaiter: newCountingWaiter(def), id: "winner-waiter"}
	built := &faultyWaiter{countingWaiter: newCountingWaiter(def), id: "discarded-waiter"}

	// the winner appears DURING the unlocked build, so this registration loses
	// and joins it — successfully, this time.
	build := func(_ eventproc.EventHub, ep eventproc.EventProcessor,
		_ flow.EventDefinition, _ renv.EngineRuntime,
	) (eventproc.EventWaiter, error) {
		hub.m.Lock()
		hub.waiters[def.ID()] = winner
		hub.m.Unlock()

		_ = built.AddEventProcessor(ep)

		return built, nil
	}

	ep := mockeventproc.NewMockEventProcessor(t)
	ep.EXPECT().ID().Return("ep-lost-report").Maybe()

	require.NoError(t, hub.registerWaiter(ep, def, build),
		"joining the winner is a successful registration")

	out := buf.String()
	require.Contains(t, out, winner.ID(),
		"the fact must name the waiter the processor actually landed on")
	require.NotContains(t, out, built.ID(),
		"it must not name the waiter this call discarded")
}

// TestUnstoppableWaiterStaysMapped is the independent review's finding A5. The
// §1.3 fix moved the removal under the lock that observes the waiter empty,
// which is correct — but it also made the removal happen BEFORE Stop. A waiter
// that will not stop is still serving its subscription, so leaving it unmapped
// strands it: the next registration for this definition builds a second waiter
// and subscribes again, and nothing can ever reach the first.
//
// A failed unregistration must leave the registry as it found it.
func TestUnstoppableWaiterStaysMapped(t *testing.T) {
	hub, err := New(enginert.Default())
	require.NoError(t, err)
	require.NoError(t, hub.Start(context.Background()))

	def, err := events.NewTerminateEventDefinition()
	require.NoError(t, err)

	w := &faultyWaiter{
		countingWaiter: newCountingWaiter(def),
		stopErr:        errors.New("the waiter will not stop"),
	}

	ep := mockeventproc.NewMockEventProcessor(t)
	ep.EXPECT().ID().Return("ep-unstoppable").Maybe()

	require.NoError(t, w.AddEventProcessor(ep))
	require.NoError(t, w.Service(context.Background())) // so Stop is attempted

	hub.m.Lock()
	hub.waiters[def.ID()] = w
	hub.m.Unlock()

	err = hub.UnregisterEvent(ep, def.ID())
	require.Error(t, err, "a waiter that will not stop is a failed unregister")
	require.ErrorContains(t, err, "waiter stop failed")

	hub.m.Lock()
	got, mapped := hub.waiters[def.ID()]
	hub.m.Unlock()

	require.True(t, mapped,
		"the waiter is still alive, so it must still be reachable")
	require.Equal(t, eventproc.EventWaiter(w), got,
		"and it is the same waiter, not a replacement")
}
