package localdispatcher_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/clock/clocktest"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/tasks"
	"github.com/dr-dobermann/gobpm/pkg/tasks/localdispatcher"
	"github.com/stretchr/testify/require"
)

var base = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// recordSink is a JobCompletionSink that records delivered outcomes.
type recordSink struct {
	mu       sync.Mutex
	outcomes []*tasks.WorkerOutcome
}

func (s *recordSink) ReportJobCompletion(
	_ context.Context, o *tasks.WorkerOutcome,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.outcomes = append(s.outcomes, o)

	return nil
}

func (s *recordSink) last() *tasks.WorkerOutcome {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.outcomes) == 0 {
		return nil
	}

	return s.outcomes[len(s.outcomes)-1]
}

// TestLocalDispatcherNewDefaults: a nil clock and a non-positive maxLock fall
// back to the bundled defaults.
func TestLocalDispatcherNewDefaults(t *testing.T) {
	require.NotNil(t, localdispatcher.New(nil, 0))
}

func topics(tt ...tasks.Topic) []tasks.Topic { return tt }

func newJob(id tasks.JobID, topic tasks.Topic) tasks.Job {
	return tasks.Job{ID: id, Topic: topic}
}

// TestLocalDispatcherEnqueueFetchComplete: the happy queue path — enqueue,
// fetch-and-lock, complete delivers a WorkerOutcome to the sink (FR-1, FR-2).
func TestLocalDispatcherEnqueueFetchComplete(t *testing.T) {
	sink := &recordSink{}
	d := localdispatcher.New(clocktest.New(base), time.Minute)
	d.BindSink(sink)

	ctx := context.Background()
	require.NoError(t, d.Enqueue(ctx, newJob("j1", "charge")))

	jobs, err := d.FetchAndLock(ctx, "w1", topics("charge"), time.Minute)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	require.Equal(t, tasks.JobID("j1"), jobs[0].ID)
	require.Equal(t, tasks.WorkerID("w1"), jobs[0].WorkerID)

	require.NoError(t, d.Complete(ctx, "j1", "w1", nil))
	require.NotNil(t, sink.last())
	require.Equal(t, tasks.JobID("j1"), sink.last().JobID())
	require.Equal(t, tasks.OutcomeComplete, sink.last().Kind())
}

// TestLocalDispatcherFetchWakesOnEnqueue: a fetcher blocked on an empty topic
// is woken by a later Enqueue and returns the new job (the broadcast wake the
// worker pool relies on) (FR-2).
func TestLocalDispatcherFetchWakesOnEnqueue(t *testing.T) {
	d := localdispatcher.New(clocktest.New(base), time.Minute)
	ctx := t.Context()

	type res struct {
		jobs []tasks.LockedJob
		err  error
	}

	got := make(chan res, 1)
	go func() {
		jobs, err := d.FetchAndLock(ctx, "w1", topics("charge"), time.Minute)
		got <- res{jobs, err}
	}()

	// let the fetcher reach the select (no job yet), then enqueue.
	time.Sleep(30 * time.Millisecond)
	require.NoError(t, d.Enqueue(ctx, newJob("j1", "charge")))

	select {
	case r := <-got:
		require.NoError(t, r.err)
		require.Equal(t, tasks.JobID("j1"), r.jobs[0].ID)
	case <-time.After(time.Second):
		t.Fatal("fetcher didn't wake on enqueue")
	}
}

// TestLocalDispatcherFailDeliversCause: Fail delivers an outcome carrying the
// technical cause (FR-8).
func TestLocalDispatcherFailDeliversCause(t *testing.T) {
	sink := &recordSink{}
	d := localdispatcher.New(clocktest.New(base), time.Minute)
	d.BindSink(sink)

	ctx := context.Background()
	require.NoError(t, d.Enqueue(ctx, newJob("j1", "charge")))
	_, err := d.FetchAndLock(ctx, "w1", topics("charge"), time.Minute)
	require.NoError(t, err)

	require.NoError(t, d.Fail(ctx, "j1", "w1",
		tasks.Fault{Cause: errors.New("upstream 503")}))
	require.ErrorContains(t, sink.last().Fault().Cause, "upstream 503")
}

// TestLocalDispatcherLockExpiryRefetch: a locked job whose lock expires becomes
// fetchable again by another worker (FR-2, NFR-3 crash-resilience).
func TestLocalDispatcherLockExpiryRefetch(t *testing.T) {
	clk := clocktest.New(base)
	d := localdispatcher.New(clk, time.Hour)

	ctx := context.Background()
	require.NoError(t, d.Enqueue(ctx, newJob("j1", "charge")))

	_, err := d.FetchAndLock(ctx, "wA", topics("charge"), 10*time.Second)
	require.NoError(t, err)

	// while locked-and-unexpired, another worker finds nothing.
	cctx, cancel := context.WithTimeout(ctx, 40*time.Millisecond)
	_, err = d.FetchAndLock(cctx, "wB", topics("charge"), 10*time.Second)
	cancel()
	require.ErrorIs(t, err, context.DeadlineExceeded)

	// past the lock, wB re-fetches it.
	clk.Advance(11 * time.Second)

	jobs, err := d.FetchAndLock(ctx, "wB", topics("charge"), 10*time.Second)
	require.NoError(t, err)
	require.Equal(t, tasks.WorkerID("wB"), jobs[0].WorkerID)
}

// TestLocalDispatcherExtendLockHolderOnlyAndCap: only the holder can extend, and
// extension is bounded by maxLockDuration (FR-2, NFR-3 liveness).
func TestLocalDispatcherExtendLockHolderOnlyAndCap(t *testing.T) {
	clk := clocktest.New(base)
	d := localdispatcher.New(clk, 30*time.Second) // maxLock cap

	ctx := context.Background()
	require.NoError(t, d.Enqueue(ctx, newJob("j1", "charge")))
	_, err := d.FetchAndLock(ctx, "wA", topics("charge"), 10*time.Second)
	require.NoError(t, err)

	// a non-holder can't extend.
	require.ErrorIs(t,
		d.ExtendLock(ctx, "j1", "wB", 5*time.Second),
		localdispatcher.ErrNotLockHolder)

	// the holder extends within the cap (base+20s <= base+30s).
	require.NoError(t, d.ExtendLock(ctx, "j1", "wA", 20*time.Second))

	// extension past the cap (base+40s > base+30s) is refused.
	require.ErrorIs(t,
		d.ExtendLock(ctx, "j1", "wA", 40*time.Second),
		localdispatcher.ErrMaxLockExceeded)
}

// TestLocalDispatcherFetchOnlyRequestedTopics: FetchAndLock returns only jobs
// for the requested topics (FR-2).
func TestLocalDispatcherFetchOnlyRequestedTopics(t *testing.T) {
	d := localdispatcher.New(clocktest.New(base), time.Minute)

	ctx := context.Background()
	require.NoError(t, d.Enqueue(ctx, newJob("j1", "email")))

	cctx, cancel := context.WithTimeout(ctx, 40*time.Millisecond)
	_, err := d.FetchAndLock(cctx, "w1", topics("charge"), time.Minute)
	cancel()
	require.ErrorIs(t, err, context.DeadlineExceeded)

	jobs, err := d.FetchAndLock(ctx, "w1", topics("email"), time.Minute)
	require.NoError(t, err)
	require.Equal(t, tasks.JobID("j1"), jobs[0].ID)
}

// TestLocalDispatcherEnqueueValidation: enqueue rejects empty ID/topic and a
// duplicate ID (FR-1).
func TestLocalDispatcherEnqueueValidation(t *testing.T) {
	d := localdispatcher.New(clocktest.New(base), time.Minute)
	ctx := context.Background()

	require.ErrorIs(t, d.Enqueue(ctx, newJob("", "charge")),
		localdispatcher.ErrEmptyJobID)
	require.ErrorIs(t, d.Enqueue(ctx, newJob("j1", "")),
		localdispatcher.ErrEmptyTopic)

	require.NoError(t, d.Enqueue(ctx, newJob("j1", "charge")))
	require.ErrorIs(t, d.Enqueue(ctx, newJob("j1", "charge")),
		localdispatcher.ErrDuplicateJob)
}

// TestLocalDispatcherReportGuards: Complete/Fail reject a foreign worker, an
// unknown job, an expired lock, and (Complete) a missing sink (FR-2, FR-6).
func TestLocalDispatcherReportGuards(t *testing.T) {
	clk := clocktest.New(base)
	d := localdispatcher.New(clk, time.Hour)
	ctx := context.Background()

	// no sink bound yet.
	require.NoError(t, d.Enqueue(ctx, newJob("j1", "charge")))
	_, err := d.FetchAndLock(ctx, "wA", topics("charge"), 10*time.Second)
	require.NoError(t, err)

	require.ErrorIs(t, d.Complete(ctx, "j1", "wA", nil),
		localdispatcher.ErrNoSink)

	// unknown job / foreign worker.
	require.ErrorIs(t, d.Complete(ctx, "missing", "wA", nil),
		localdispatcher.ErrJobNotFound)
	require.ErrorIs(t, d.Complete(ctx, "j1", "wB", nil),
		localdispatcher.ErrNotLockHolder)

	// Fail runs the same lock guard before it classifies (SRD-038).
	require.ErrorIs(t, d.Fail(ctx, "missing", "wA", tasks.Fault{}),
		localdispatcher.ErrJobNotFound)

	// past the lock, the holder can no longer complete.
	clk.Advance(11 * time.Second)
	require.ErrorIs(t, d.Complete(ctx, "j1", "wA", nil),
		localdispatcher.ErrLockExpired)
}

// TestLocalDispatcherRegisterWorkerValidation: RegisterWorker rejects an empty
// topic, a nil worker, and a duplicate topic (FR-2).
func TestLocalDispatcherRegisterWorkerValidation(t *testing.T) {
	d := localdispatcher.New(clocktest.New(base), time.Minute)
	ctx := t.Context()

	fn := func(context.Context, tasks.LockedJob) (*data.ItemDefinition, error) {
		return nil, nil
	}

	require.ErrorIs(t, d.RegisterWorker(ctx, "", fn),
		localdispatcher.ErrEmptyTopic)
	require.ErrorIs(t, d.RegisterWorker(ctx, "charge", nil),
		localdispatcher.ErrNilWorker)

	require.NoError(t, d.RegisterWorker(ctx, "charge", fn))
	require.ErrorIs(t, d.RegisterWorker(ctx, "charge", fn),
		localdispatcher.ErrDuplicateWorker)
}

// TestLocalDispatcherWorkerPoolRunsHandler: a registered worker fetch-and-locks
// an enqueued job, runs its handler, and reports Complete to the sink — the
// batteries-included local pool (FR-2). Uses the real clock.
func TestLocalDispatcherWorkerPoolRunsHandler(t *testing.T) {
	sink := &recordSink{}
	d := localdispatcher.New(nil, time.Minute)
	d.BindSink(sink)

	ctx := t.Context()

	var ran tasks.JobID

	var mu sync.Mutex

	require.NoError(t, d.RegisterWorker(ctx, "charge",
		func(_ context.Context, j tasks.LockedJob) (*data.ItemDefinition, error) {
			mu.Lock()
			ran = j.ID
			mu.Unlock()

			return nil, nil
		}))

	require.NoError(t, d.Enqueue(ctx, newJob("j1", "charge")))

	require.Eventually(t, func() bool { return sink.last() != nil },
		2*time.Second, 5*time.Millisecond)
	require.Equal(t, tasks.JobID("j1"), sink.last().JobID())

	mu.Lock()
	require.Equal(t, tasks.JobID("j1"), ran)
	mu.Unlock()
}

// TestLocalDispatcherWorkerPoolReportsFail: a worker handler that errors makes
// the pool report Fail, delivering the cause to the sink (FR-2, FR-8).
func TestLocalDispatcherWorkerPoolReportsFail(t *testing.T) {
	sink := &recordSink{}
	d := localdispatcher.New(nil, time.Minute)
	d.BindSink(sink)

	ctx := t.Context()

	require.NoError(t, d.RegisterWorker(ctx, "charge",
		func(context.Context, tasks.LockedJob) (*data.ItemDefinition, error) {
			return nil, errors.New("worker boom")
		}))

	require.NoError(t, d.Enqueue(ctx, newJob("j1", "charge")))

	require.Eventually(t, func() bool { return sink.last() != nil },
		2*time.Second, 5*time.Millisecond)
	require.ErrorContains(t, sink.last().Fault().Cause, "worker boom")
}

// spyLogger records log messages to verify the dispatcher uses a bound logger.
type spyLogger struct {
	mu   sync.Mutex
	msgs []string
}

func (l *spyLogger) record(msg string) {
	l.mu.Lock()
	l.msgs = append(l.msgs, msg)
	l.mu.Unlock()
}

func (l *spyLogger) Debug(msg string, _ ...any) { l.record(msg) }
func (l *spyLogger) Info(msg string, _ ...any)  { l.record(msg) }
func (l *spyLogger) Warn(msg string, _ ...any)  { l.record(msg) }
func (l *spyLogger) Error(msg string, _ ...any) { l.record(msg) }

func (l *spyLogger) has(msg string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	for _, m := range l.msgs {
		if m == msg {
			return true
		}
	}

	return false
}

// TestLocalDispatcherReportBpmnError: a worker's Business Error is delivered to
// the sink as an OutcomeBpmnError (SRD-037 FR-2).
func TestLocalDispatcherReportBpmnError(t *testing.T) {
	sink := &recordSink{}
	d := localdispatcher.New(clocktest.New(base), time.Minute)
	d.BindSink(sink)

	ctx := context.Background()
	require.NoError(t, d.Enqueue(ctx, newJob("j1", "charge")))
	_, err := d.FetchAndLock(ctx, "w1", topics("charge"), time.Minute)
	require.NoError(t, err)

	require.NoError(t, d.ReportBpmnError(ctx, "j1", "w1", "Conflict", "dup"))
	require.Equal(t, tasks.OutcomeBpmnError, sink.last().Kind())

	code, msg := sink.last().BpmnError()
	require.Equal(t, "Conflict", code)
	require.Equal(t, "dup", msg)
}

// TestLocalDispatcherReportStatus: a worker's Business Status is delivered to the
// sink as an OutcomeStatus carrying the value (SRD-037 FR-2).
func TestLocalDispatcherReportStatus(t *testing.T) {
	sink := &recordSink{}
	d := localdispatcher.New(clocktest.New(base), time.Minute)
	d.BindSink(sink)

	ctx := context.Background()
	require.NoError(t, d.Enqueue(ctx, newJob("j1", "charge")))
	_, err := d.FetchAndLock(ctx, "w1", topics("charge"), time.Minute)
	require.NoError(t, err)

	require.NoError(t, d.ReportStatus(ctx, "j1", "w1",
		values.NewVariable("NOT_FOUND")))
	require.Equal(t, tasks.OutcomeStatus, sink.last().Kind())
	require.Equal(t, "NOT_FOUND", sink.last().StatusValue().Get(ctx))
}

// TestLocalDispatcherBindLogger: a bound logger is used for lifecycle logging; a
// nil logger is ignored (the default is kept).
func TestLocalDispatcherBindLogger(t *testing.T) {
	lg := &spyLogger{}
	d := localdispatcher.New(clocktest.New(base), time.Minute)
	d.BindLogger(nil) // ignored — keeps the default
	d.BindLogger(lg)  // now uses lg

	require.NoError(t, d.Enqueue(context.Background(), newJob("j1", "charge")))
	require.True(t, lg.has("job enqueued"),
		"the bound logger receives the lifecycle log")
}
