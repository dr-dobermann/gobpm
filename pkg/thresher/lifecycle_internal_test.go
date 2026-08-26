package thresher

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/pkg/datastore"
	"github.com/dr-dobermann/gobpm/pkg/datastore/memstore"
	"github.com/dr-dobermann/gobpm/pkg/messaging"
	"github.com/dr-dobermann/gobpm/pkg/messaging/membroker"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/dr-dobermann/gobpm/pkg/observability/memmetrics"
	"github.com/dr-dobermann/gobpm/pkg/renv"
	"github.com/dr-dobermann/gobpm/pkg/repository"
	"github.com/dr-dobermann/gobpm/pkg/repository/memrepo"
	"github.com/stretchr/testify/require"
)

// runErrHub is an EventHub whose Start succeeds and whose Run returns a
// non-context error; every other method is the embedded nil interface (none is
// reached — Run with no registered processes only calls Start then Run).
type runErrHub struct {
	eventproc.EventHub

	runErr error
}

func (runErrHub) Start(context.Context) error { return nil }

func (h runErrHub) Run(context.Context) error { return h.runErr }

// captureLogger records Error() messages on a channel; Debug/Info/Warn no-op.
type captureLogger struct {
	errs chan string
}

func (captureLogger) Debug(string, ...any) {}
func (captureLogger) Info(string, ...any)  {}
func (captureLogger) Warn(string, ...any)  {}

func (c captureLogger) Error(msg string, _ ...any) {
	select {
	case c.errs <- msg:
	default:
	}
}

// TestEventHubRunErrorLogged covers FIX-013 §1.5: a non-context EventHub.Run
// error is surfaced to the logger instead of being discarded.
func TestEventHubRunErrorLogged(t *testing.T) {
	cl := captureLogger{errs: make(chan string, 1)}

	th, err := New("lc-runerr", WithLogger(cl))
	require.NoError(t, err)

	// swap in a hub whose Run loop fails with a non-context error.
	th.eventHub = runErrHub{runErr: errors.New("hub boom")}

	require.NoError(t, th.Run(context.Background()))

	select {
	case msg := <-cl.errs:
		require.Equal(t, "event hub run loop failed", msg)
	case <-time.After(2 * time.Second):
		t.Fatal("EventHub.Run error was not logged")
	}
}

// failStartHub is an EventHub whose Start fails; every other method is the
// embedded nil interface (none is reached, because Run rolls back at Start).
type failStartHub struct {
	eventproc.EventHub

	err error
}

func (h failStartHub) Start(context.Context) error {
	return h.err
}

// TestStateLockFreeUnderHeldMutex verifies State() does not acquire t.m, so it
// is callable while t.m is held without deadlocking — the property that removes
// the FIX-002 RC2 re-entrant self-deadlock vector (SRD-031.B FR-3, T-2). If
// State still locked, this test would hang on the held mutex and fail by
// timeout.
func TestStateLockFreeUnderHeldMutex(t *testing.T) {
	th, err := New("lc-lockfree")
	require.NoError(t, err)

	th.state.Store(uint32(Started))

	th.m.Lock()
	got := th.State()
	th.m.Unlock()

	require.Equal(t, Started, got)
}

// TestShutdownWhileStartingRejected verifies Shutdown rejects when the engine is
// mid-start (Starting): it cannot tear down a transition that has not completed,
// and the state is left untouched (SRD-031.B FR-6, T-9).
func TestShutdownWhileStartingRejected(t *testing.T) {
	th, err := New("lc-starting")
	require.NoError(t, err)

	th.state.Store(uint32(Starting))

	err = th.Shutdown(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "starting")
	require.Equal(t, Starting, th.State())
}

// TestShutdownFromInvalidRejected verifies the defensive default branch: an
// Invalid state (unreachable in the normal lifecycle) is rejected rather than
// silently torn down (SRD-031.B FR-6, T-9 sibling).
func TestShutdownFromInvalidRejected(t *testing.T) {
	th, err := New("lc-invalid")
	require.NoError(t, err)

	th.state.Store(uint32(Invalid))

	err = th.Shutdown(context.Background())
	require.Error(t, err)
	require.Equal(t, Invalid, th.State())
}

// regFailHub starts cleanly and runs until its context is canceled, but fails
// every persistent-event registration — it drives the registerAllStarters
// failure path in Run while leaving Start (so the engine reaches Started) and
// Run (so the hub goroutine is live and must be torn down) intact.
type regFailHub struct {
	eventproc.EventHub

	regErr error
}

func (regFailHub) Start(context.Context) error { return nil }

func (regFailHub) Run(ctx context.Context) error {
	<-ctx.Done()

	return ctx.Err()
}

func (h regFailHub) RegisterPersistentEvent(
	eventproc.EventProcessor, flow.EventDefinition,
) error {
	return h.regErr
}

// TestRunRollsBackWhenStarterRegistrationFails covers FIX-013 §1.2 (audit
// third-pass §2.7): when registerAllStarters fails after Started is published,
// Run must roll the lifecycle back to NotStarted and stop the hub goroutine,
// instead of stranding a half-started engine that rejects a retry and leaves
// Shutdown to tear down a half-wired engine.
func TestRunRollsBackWhenStarterRegistrationFails(t *testing.T) {
	th, err := New("lc-starter-rollback")
	require.NoError(t, err)

	// Seed one registered process whose single starter will fail to subscribe,
	// so registerAllStarters returns an error after Started is published.
	th.registrations["k"] = []*ProcessRegistration{
		{
			key:      "k",
			id:       "r1",
			version:  1,
			starters: []*instanceStarter{mkStarter(t, "x")},
		},
	}

	th.eventHub = regFailHub{regErr: errors.New("subscribe boom")}

	err = th.Run(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "couldn't register instance-starters")
	require.Equal(t, NotStarted, th.State())

	// Re-runnable: the rollback left the CAS claim free, so a second Run is not
	// rejected by the state machine — it reaches the hub and fails the same way.
	err = th.Run(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "couldn't register instance-starters")
	require.Equal(t, NotStarted, th.State())
}

// TestRunRollsBackOnHubStartFailure verifies that when hub.Start fails, Run
// rolls the claimed Starting transition back to NotStarted (so the engine is not
// stranded mid-start) and stays re-runnable: a second Run passes the
// NotStarted->Starting claim again and reaches the hub, failing with the hub
// error rather than an "already started" rejection (SRD-031.B FR-6, T-10).
func TestRunRollsBackOnHubStartFailure(t *testing.T) {
	th, err := New("lc-rollback")
	require.NoError(t, err)

	th.eventHub = failStartHub{err: errors.New("hub boom")}

	err = th.Run(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "couldn't start eventHub")
	require.Equal(t, NotStarted, th.State())

	// Re-runnable: not locked out by the state machine.
	err = th.Run(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "couldn't start eventHub")
	require.Equal(t, NotStarted, th.State())
}

// TestEngineContextIsRaceFree is FIX-036 T-1: the engine context is published
// and read as one atomic pair, so a Run racing a Shutdown (and the launch
// paths reading between them) carries no data race — the defect was a field
// written unlocked by Run and read under m by Shutdown, which is a race
// however well the reader locks.
func TestEngineContextIsRaceFree(t *testing.T) {
	th, err := New("lc-engine-ctx", WithoutBanner())
	require.NoError(t, err)

	var wg sync.WaitGroup

	// One writer (Run) against many readers — the shape the race detector
	// needs to see. Readers use the same accessor every launch path uses.
	//
	// The barrier is load-bearing: Run is NON-blocking, so without one the
	// writer goroutine could finish before the runtime schedules a single
	// reader, and the detector only reports accesses that actually overlap.
	// The test would then pass for the racy shape it exists to catch — its
	// whole value is as a canary for someone reverting the atomic pair back
	// to plain fields.
	start := make(chan struct{})

	wg.Add(1)

	go func() {
		defer wg.Done()

		<-start

		_ = th.Run(context.Background())
	}()

	for range 8 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			<-start

			for range 50 {
				if ctx, ok := th.engineContext(); ok {
					_ = ctx.Err()
				}
			}
		}()
	}

	close(start) // fire all nine together

	wg.Wait()

	// A Shutdown after a completed Run must cancel what Run published —
	// the failure mode of the racy read was a nil cancel that tore down
	// nothing while reporting Stopped.
	ec := th.engine.Load()
	require.NotNil(t, ec, "Run must publish the engine pair")

	require.NoError(t, th.Shutdown(context.Background()))
	require.Error(t, ec.ctx.Err(), "Shutdown must cancel the published context")
}

// TestEngineContextRefusedBeforeRun is FIX-036 T-1's sibling: before Run there
// is no engine lifetime to hang work on, so the accessor says so and the launch
// paths turn that into a classified error instead of dereferencing nil.
func TestEngineContextRefusedBeforeRun(t *testing.T) {
	th, err := New("lc-engine-ctx-unrun")
	require.NoError(t, err)

	_, ok := th.engineContext()
	require.False(t, ok)

	err = th.errEngineNotRunning("probe")
	require.Error(t, err)
	require.Contains(t, err.Error(), "isn't running")

	// Every launch path derives its instance context through one helper, so
	// the refusal is pinned once: a launch on an engine that never ran returns
	// a classified error naming the caller instead of dereferencing the absent
	// pair. The other three callers (InvokeProcess, launchInstanceFromEvent,
	// recovery) are all gated on a started engine long before they get here.
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)

	proc := blockingTaskProcess(t, "p-unrun", entered, release)
	_, err = th.RegisterProcess(proc)
	require.NoError(t, err)

	snap := th.latestSnapshotLocked(proc.ID())
	require.NotNil(t, snap)

	_, err = th.launchInstance(snap, nil)
	require.ErrorContains(t, err, "launchInstance")
	require.ErrorContains(t, err, "isn't running")

	_, _, err = th.instanceContext("probe-op")
	require.ErrorContains(t, err, "probe-op")
}

// TestUpdateStateRejectsLifecycleJumps is FIX-036 T-6: UpdateState is the
// OPERATOR's door (pause/resume), not a second way into the lifecycle ladder
// Run and Shutdown claim with a CAS. It used to accept any legal enum member,
// so a host could store Started on an engine that never ran — after which
// RegisterEvent's `State() != Started` guard admitted registrations to a hub
// that was never started.
func TestUpdateStateRejectsLifecycleJumps(t *testing.T) {
	th, err := New("engine-transitions", WithoutBanner(), WithoutStartupConfig())
	require.NoError(t, err)

	require.Equal(t, NotStarted, th.State())

	for _, ns := range []State{Started, Starting, Stopping, Stopped, Paused} {
		require.Error(t, th.UpdateState(ns),
			"a never-run engine may not be moved to %q by hand", ns)
		require.Equal(t, NotStarted, th.State(),
			"a refused transition leaves the state untouched")
	}

	// an out-of-range value is still refused by the enum check, ahead of the
	// transition rule.
	require.Error(t, th.UpdateState(State(200)))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, th.Run(ctx))

	// the operator pair, both ways.
	require.NoError(t, th.UpdateState(Paused))
	require.Equal(t, Paused, th.State())
	require.NoError(t, th.UpdateState(Started))
	require.Equal(t, Started, th.State())

	// but not the lifecycle transitions, even from a legal running state.
	for _, ns := range []State{Stopped, Stopping, Starting, NotStarted} {
		require.Error(t, th.UpdateState(ns))
		require.Equal(t, Started, th.State())
	}
}

// blockingTaskProcess builds start → service task → end, where the task
// signals that it has been ENTERED and then blocks until release is closed —
// ignoring its context on purpose, so the instance cannot settle on its own.
func blockingTaskProcess(
	t *testing.T, name string, entered chan<- struct{}, release <-chan struct{},
) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	op, err := gooper.New(name,
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			close(entered)
			<-release

			return nil, nil
		})
	require.NoError(t, err)

	st, err := activities.NewServiceTask(name, op, activities.WithoutParams())
	require.NoError(t, err)

	proc, err := process.New(name)
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, st, end} {
		require.NoError(t, proc.Add(e))
	}

	_, err = flow.Link(start, st)
	require.NoError(t, err)
	_, err = flow.Link(st, end)
	require.NoError(t, err)

	return proc
}

// TestShutdownAwaitsInstanceBornDuringCancel is FIX-036 T-7: Shutdown used to
// snapshot the instance registry BEFORE cancelling the engine context and then
// await only that snapshot, so an instance born in the window between the two —
// an event-triggered start the hub was already dispatching, a Call Activity
// child its parent was already creating — was left running.
//
// The window is reproduced exactly rather than approximately: the engine's own
// published context/cancel pair is replaced with one whose cancel launches an
// instance first. That is the seam Shutdown reads, so the birth lands precisely
// between the snapshot and the cancel.
func TestShutdownAwaitsInstanceBornDuringCancel(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)

	th, err := New("engine-shutdown-window",
		WithoutBanner(), WithoutStartupConfig())
	require.NoError(t, err)

	proc := blockingTaskProcess(t, "p-block", entered, release)
	_, err = th.RegisterProcess(proc)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, th.Run(ctx))

	ec := th.engine.Load()
	require.NotNil(t, ec)

	var bornID string

	th.engine.Store(&engineCtx{ctx: ec.ctx, cancel: func() {
		h, lerr := th.launchInstance(th.latestSnapshotLocked(proc.ID()), nil)
		if lerr == nil {
			bornID = h.ID()
			<-entered // it is inside the blocking task, so it cannot settle
		}

		ec.cancel()
	}})

	sctx, scancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer scancel()

	err = th.Shutdown(sctx)

	require.Error(t, err,
		"an instance born in the cancel window must still be awaited")
	require.ErrorContains(t, err, "1 instance(s) settled")
	require.ErrorContains(t, err, bornID,
		"the timeout names what did not settle")
}

// TestUnmappedEngineStateReportsNothing pins reportEngineState's no-phase early
// return. A state with no observable phase — NotStarted, which Run's rollback
// stores — reports nothing; a mapped one reports. This used to be driven
// through UpdateState(NotStarted), which the transition rule now refuses
// (FIX-036 §1.6), so it is pinned at the reporter instead of through a door
// that no longer opens.
func TestUnmappedEngineStateReportsNothing(t *testing.T) {
	th, err := New("engine-unmapped", WithoutBanner(), WithoutStartupConfig())
	require.NoError(t, err)

	o := &recObserver{}
	sub := th.Observe(o)

	th.reportEngineState(NotStarted)
	th.reportEngineState(Invalid)
	th.reportEngineState(Paused)

	sub.Cancel()

	require.Equal(t, 1, o.count(), "only the mapped state reports")
}

// journal records what the fakes did, in the order they did it, so a test can
// assert sequence rather than merely counting calls.
type journal struct {
	mu   sync.Mutex
	seen []string
}

func (j *journal) note(what string) {
	j.mu.Lock()
	defer j.mu.Unlock()

	j.seen = append(j.seen, what)
}

func (j *journal) entries() []string {
	j.mu.Lock()
	defer j.mu.Unlock()

	return append([]string{}, j.seen...)
}

// lifecycleRepo is a Repository that also implements Starter and Stopper. It
// embeds the real in-memory default, so it is a Repository in full and only its
// lifecycle behavior is fake.
type lifecycleRepo struct {
	repository.Repository

	j        *journal
	startErr error
	stopErr  error
	stops    int
}

func (r *lifecycleRepo) Start(context.Context) error {
	r.j.note("repo.Start")

	return r.startErr
}

func (r *lifecycleRepo) Stop(context.Context) error {
	r.stops++
	r.j.note("repo.Stop")

	// Idempotent by contract (FR-3): a second call is a no-op returning nil.
	if r.stops > 1 {
		return nil
	}

	return r.stopErr
}

// lifecycleBroker is a MessageBroker that implements Stopper only — the seam
// that must stop FIRST, so the order claim has two ends to check.
type lifecycleBroker struct {
	messaging.MessageBroker

	j       *journal
	stopErr error
}

func (b *lifecycleBroker) Stop(context.Context) error {
	b.j.note("broker.Stop")

	return b.stopErr
}

// plainRepo implements neither capability. It exists to prove the hooks are
// optional: a seam that does not opt in is never called and never fails a run.
type plainRepo struct{ repository.Repository }

func lifecycleEngine(t *testing.T, name string, opts ...Option) *Thresher {
	t.Helper()

	th, err := New(name, append([]Option{
		WithoutBanner(), WithoutStartupConfig(),
	}, opts...)...)
	require.NoError(t, err)

	return th
}

// TestStarterIsCalledBeforeWork is T-1: a seam implementing renv.Starter is
// started exactly once, and before the engine reports itself Started.
func TestStarterIsCalledBeforeWork(t *testing.T) {
	j := &journal{}
	repo := &lifecycleRepo{Repository: memrepo.New(), j: j}

	th := lifecycleEngine(t, "start-hook", WithRepository(repo))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, th.Run(ctx))
	defer func() { _ = th.Shutdown(context.Background()) }()

	require.Equal(t, []string{"repo.Start"}, j.entries(),
		"the seam is started once, during Run")
	require.Equal(t, Started, th.State())
}

// TestStarterFailureAbortsRun is T-2. An extension that cannot start is not a
// degraded mode: the run fails, the error names the seam, and the engine does
// not report itself Started.
func TestStarterFailureAbortsRun(t *testing.T) {
	j := &journal{}
	repo := &lifecycleRepo{
		Repository: memrepo.New(),
		j:          j,
		startErr:   errors.New("the pool will not open"),
	}

	th := lifecycleEngine(t, "start-fail", WithRepository(repo))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := th.Run(ctx)
	require.Error(t, err)
	require.ErrorContains(t, err, "Repository",
		"the failure names WHICH seam refused")
	require.ErrorContains(t, err, "the pool will not open")
	require.NotEqual(t, Started, th.State())
}

// TestStopOrderIsBrokerBeforeRepository is T-3, and it is the claim the whole
// design rests on: shutdown order is a correctness requirement, not tidiness.
// The broker must stop accepting before the repository closes, or in-flight
// state is lost — which a generic sweep over an unordered plugin list could not
// express.
func TestStopOrderIsBrokerBeforeRepository(t *testing.T) {
	j := &journal{}
	repo := &lifecycleRepo{Repository: memrepo.New(), j: j}
	broker := &lifecycleBroker{MessageBroker: membroker.New(), j: j}

	th := lifecycleEngine(t, "stop-order",
		WithRepository(repo), WithMessageBroker(broker))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, th.Run(ctx))
	require.NoError(t, th.Shutdown(context.Background()))

	require.Equal(t, []string{"repo.Start", "broker.Stop", "repo.Stop"},
		j.entries(),
		"input closes before storage; storage stops after the work that "+
			"checkpoints into it")
}

// TestStopFailuresAreJoinedAndDoNotAbort is T-4. A failing Stop must not
// abandon the seams after it — the caller is shutting down precisely because it
// wants everything released.
func TestStopFailuresAreJoinedAndDoNotAbort(t *testing.T) {
	j := &journal{}
	repo := &lifecycleRepo{
		Repository: memrepo.New(), j: j,
		stopErr: errors.New("repo will not close"),
	}
	broker := &lifecycleBroker{
		MessageBroker: membroker.New(), j: j,
		stopErr: errors.New("broker will not close"),
	}

	th := lifecycleEngine(t, "stop-join",
		WithRepository(repo), WithMessageBroker(broker))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, th.Run(ctx))

	err := th.Shutdown(context.Background())
	require.Error(t, err)
	require.ErrorContains(t, err, "broker will not close")
	require.ErrorContains(t, err, "repo will not close",
		"the second failure is not abandoned by the first")

	require.Contains(t, j.entries(), "repo.Stop",
		"the seam after the failing one was still stopped")
}

// TestStopIsIdempotent is T-5. The engine stops what it holds, while a host
// that constructed the adapter may stop it too; idempotency is what makes that
// overlap safe rather than a double release.
func TestStopIsIdempotent(t *testing.T) {
	j := &journal{}
	repo := &lifecycleRepo{Repository: memrepo.New(), j: j}

	th := lifecycleEngine(t, "stop-idempotent", WithRepository(repo))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, th.Run(ctx))
	require.NoError(t, th.Shutdown(context.Background()))
	require.NoError(t, th.Shutdown(context.Background()),
		"a second Shutdown is a no-op")

	require.NoError(t, repo.Stop(context.Background()),
		"and a second Stop on the adapter itself returns nil")
}

// TestSeamWithoutHooksIsUntouched is T-6, and it is what makes the capabilities
// OPTIONAL rather than a hidden requirement: a seam implementing neither
// interface is never called and never fails a run.
func TestSeamWithoutHooksIsUntouched(t *testing.T) {
	th := lifecycleEngine(t, "no-hooks",
		WithRepository(&plainRepo{Repository: memrepo.New()}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, th.Run(ctx), "a seam with no hooks does not fail the run")
	require.NoError(t, th.Shutdown(context.Background()))
}

// runtimeAwareRepo captures the runtime the engine hands it, so a test can
// check WHICH runtime arrived rather than merely that one did.
type runtimeAwareRepo struct {
	repository.Repository

	got renv.EngineRuntime
}

func (r *runtimeAwareRepo) UseRuntime(rt renv.EngineRuntime) { r.got = rt }

// healthRepo reports the health it is told to.
type healthRepo struct {
	repository.Repository

	err error
}

func (h *healthRepo) HealthCheck(context.Context) error { return h.err }

// TestUseRuntimeReceivesTheResolvedRuntime is T-7. The point of Pattern C is
// that an adapter can default a dependency from the engine and emit through the
// engine's own observability — which only works if what arrives is the RESOLVED
// runtime, carrying the configured recorder rather than a bundled default.
func TestUseRuntimeReceivesTheResolvedRuntime(t *testing.T) {
	repo := &runtimeAwareRepo{Repository: memrepo.New()}
	rec := memmetrics.New()

	_ = lifecycleEngine(t, "runtime-aware",
		WithRepository(repo), WithMetricsRecorder(rec))

	require.NotNil(t, repo.got, "the seam received a runtime during New")
	require.Same(t, rec, repo.got.MetricsRecorder(),
		"and it is the CONFIGURED recorder, not a default — an adapter emitting "+
			"through it reaches the same stream as the engine")
	require.NotNil(t, repo.got.Logger())
}

// TestHealthCheckerIsReachable is T-8: a host can ask the engine whether its
// extensions are usable right now, and a failing seam is named.
func TestHealthCheckerIsReachable(t *testing.T) {
	t.Run("healthy", func(t *testing.T) {
		th := lifecycleEngine(t, "health-ok",
			WithRepository(&healthRepo{Repository: memrepo.New()}))

		require.NoError(t, th.HealthCheck(context.Background()))
	})

	t.Run("a failing seam is named", func(t *testing.T) {
		th := lifecycleEngine(t, "health-bad",
			WithRepository(&healthRepo{
				Repository: memrepo.New(),
				err:        errors.New("the pool is exhausted"),
			}))

		err := th.HealthCheck(context.Background())
		require.Error(t, err)
		require.ErrorContains(t, err, "Repository")
		require.ErrorContains(t, err, "the pool is exhausted")
	})

	t.Run("a seam without the capability is not asked", func(t *testing.T) {
		th := lifecycleEngine(t, "health-absent",
			WithRepository(&plainRepo{Repository: memrepo.New()}))

		require.NoError(t, th.HealthCheck(context.Background()),
			"silence from a seam that never opted in is not a failure")
	})
}

// lifecycleStore is a DataStore implementing Starter and Stopper, so a
// re-registered ref can be observed at the lifecycle seam.
type lifecycleStore struct {
	datastore.DataStore

	j    *journal
	name string
}

func (s *lifecycleStore) Start(context.Context) error {
	s.j.note(s.name + ".Start")

	return nil
}

func (s *lifecycleStore) Stop(context.Context) error {
	s.j.note(s.name + ".Stop")

	return nil
}

// TestStartRollsBackWhatItAlreadyStarted (pr-review #1): when a later seam's
// Start fails, the seams already started are stopped before the error returns.
//
// Without the rollback they stay live with nothing holding a reference to
// stop them — Run resets the engine to NotStarted, so the caller may start
// again and would strand the first set. It is the same failure §3.2 guards
// against on the shutdown side.
func TestStartRollsBackWhatItAlreadyStarted(t *testing.T) {
	j := &journal{}

	// The Repository starts first and succeeds; the MessageBroker starts last
	// and fails, so the repository must be rolled back.
	repo := &lifecycleRepo{Repository: memrepo.New(), j: j}
	broker := &startFailBroker{MessageBroker: membroker.New(), j: j}

	th := lifecycleEngine(t, "rollback",
		WithRepository(repo), WithMessageBroker(broker))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := th.Run(ctx)
	require.Error(t, err, "a failing Start must fail the run")
	require.Contains(t, err.Error(), "MessageBroker")

	seen := j.entries()
	require.Contains(t, seen, "repo.Start")
	require.Contains(t, seen, "repo.Stop",
		"the repository started before the broker failed, so it must be "+
			"stopped again — otherwise it stays live and unreachable")

	// Ordering: the rollback happens after the failure, not before it.
	require.Less(t, indexOf(seen, "repo.Start"), indexOf(seen, "broker.Start"))
	require.Less(t, indexOf(seen, "broker.Start"), indexOf(seen, "repo.Stop"))
}

// startFailBroker is a MessageBroker whose Start fails.
type startFailBroker struct {
	messaging.MessageBroker

	j *journal
}

func (b *startFailBroker) Start(context.Context) error {
	b.j.note("broker.Start")

	return errors.New("broker refuses to start")
}

func indexOf(ss []string, want string) int {
	for i, s := range ss {
		if s == want {
			return i
		}
	}

	return -1
}

// TestReplacedDataStoreLeavesTheLifecycle (pr-review #2): registering two
// stores under ONE ref replaces the first, and the superseded store is not
// started, health-checked or stopped.
//
// WithDataStore documents replace-by-ref, and the registry honours it; the
// lifecycle list did not, so a discarded store still had its connections
// acquired and its health folded into the engine's.
func TestReplacedDataStoreLeavesTheLifecycle(t *testing.T) {
	j := &journal{}

	first := &lifecycleStore{DataStore: memstore.New(), j: j, name: "first"}
	second := &lifecycleStore{DataStore: memstore.New(), j: j, name: "second"}

	th := lifecycleEngine(t, "store-replace",
		WithDataStore("orders", first),
		WithDataStore("orders", second))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, th.Run(ctx))
	require.NoError(t, th.Shutdown(context.Background()))

	seen := j.entries()
	require.Contains(t, seen, "second.Start")
	require.Contains(t, seen, "second.Stop")
	require.NotContains(t, seen, "first.Start",
		"the superseded store serves no reference; starting it acquires "+
			"resources nothing will use")
	require.NotContains(t, seen, "first.Stop")
}

// stopOnlyRepo implements Stopper but NOT Starter — the engine never started
// it, so a rollback must leave it alone.
//
// It has to be a REPOSITORY, not the message broker: the broker is the last
// seam, so a failure anywhere would leave it outside the started prefix and
// the assertion would hold no matter what the rollback did. The repository
// starts fourth, well inside the prefix, so skipping it is a real decision.
type stopOnlyRepo struct {
	repository.Repository

	j *journal
}

func (r *stopOnlyRepo) Stop(context.Context) error {
	r.j.note("stopOnly.Stop")

	return nil
}

// TestRollbackSkipsWhatItNeverStarted (pr-review #1, second half): the unwind
// after a failed Start touches only seams that actually started.
//
// A seam implementing Stopper but not Starter was never started here, and its
// owner did not hand the engine that responsibility — stopping it on the way
// out of a failed start would shut down something the caller is still using.
func TestRollbackSkipsWhatItNeverStarted(t *testing.T) {
	j := &journal{}

	// The repository is IN the started prefix (seam 3) but implements no
	// Starter; the message broker starts last and fails, triggering the unwind.
	repo := &stopOnlyRepo{Repository: memrepo.New(), j: j}
	broker := &startFailBroker{MessageBroker: membroker.New(), j: j}

	th := lifecycleEngine(t, "rollback-skip",
		WithRepository(repo), WithMessageBroker(broker))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.Error(t, th.Run(ctx))

	seen := j.entries()
	require.Contains(t, seen, "broker.Start", "the failing seam ran")
	require.NotContains(t, seen, "stopOnly.Stop",
		"a seam the engine never started must not be stopped by the rollback")
}

// startOnlyStore implements Starter but NOT Stopper: it starts, and there is
// nothing for the rollback to undo.
type startOnlyStore struct {
	datastore.DataStore

	j *journal
}

func (s *startOnlyStore) Start(context.Context) error {
	s.j.note("startOnly.Start")

	return nil
}

// TestRollbackReportsItsOwnFailures (pr-review #1, third half): a Stop that
// fails DURING the rollback is reported alongside the start failure, and a
// started seam with no Stopper is simply skipped.
//
// The joining is the part worth pinning: the start failure is the cause, and a
// rollback that also fails is a second fact about the same event. Returning
// only the rollback error would bury WHY the engine was unwinding, and
// returning only the start error would hide that the cleanup was incomplete —
// which is exactly what an operator needs to know before retrying.
func TestRollbackReportsItsOwnFailures(t *testing.T) {
	j := &journal{}

	repo := &lifecycleRepo{
		Repository: memrepo.New(),
		j:          j,
		stopErr:    errors.New("repo will not close"),
	}
	store := &startOnlyStore{DataStore: memstore.New(), j: j}
	broker := &startFailBroker{MessageBroker: membroker.New(), j: j}

	th := lifecycleEngine(t, "rollback-errors",
		WithRepository(repo),
		WithDataStore("orders", store),
		WithMessageBroker(broker))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := th.Run(ctx)
	require.Error(t, err)

	// Both facts survive: what refused to start, and what refused to unwind.
	require.Contains(t, err.Error(), "broker refuses to start",
		"the cause must not be replaced by the cleanup's own failure")
	require.Contains(t, err.Error(), "repo will not close",
		"a rollback that fails must say so — the engine is left dirty")
	require.Contains(t, err.Error(), "rolling back the Repository")

	seen := j.entries()
	require.Contains(t, seen, "startOnly.Start")
	require.Contains(t, seen, "repo.Stop")
}
