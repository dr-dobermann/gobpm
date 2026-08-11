// Package taskstest publishes the WorkerDispatcher conformance suite
// (ADR-003 §4.2): every dispatcher — the bundled local queue, or an adapter
// over Redis, SQS or a Camunda-style external-task API — proves the same
// fetch-and-lock contract by calling Conformance from a one-line test.
//
// Scope, and what is deliberately outside it:
//
//   - **Lock EXPIRY is not asserted.** A dispatcher's clock is its own
//     (localdispatcher takes a clock.Clock; a remote queue's deadline lives in
//     the server), so a portable suite has no way to advance it. Testing
//     expiry by sleeping would make the suite slow and flaky in exchange for
//     an assertion each adapter can make better itself.
//   - **Retry, back-off and fault CLASSIFICATION are not asserted.** Mapping a
//     Fault to a BPMN outcome is the engine's ErrorMapper, reached through a
//     bound seam; a dispatcher that merely forwards the Fault is conformant.
//
// What remains is the queue contract itself: enqueue, exclusive fetch-and-lock,
// topic filtering, holder-only operations, and the four terminal reports
// reaching the completion sink exactly once.
package taskstest

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/tasks"
)

// fetchWait bounds a FetchAndLock that must return. It is a hang-breaker, not
// a latency assertion: an adapter over a remote queue may take a network round
// trip, and waiting costs nothing when the job is already queued.
//
// It is a var rather than a const only so this package's own negative tests
// can shrink it: they drive assertions against a dispatcher that is KNOWN to
// hand out no work, which does not need five seconds to prove. Nothing
// outside this package can reach it.
var fetchWait = 5 * time.Second

// settleWait is how long a terminal report is given to reach the sink. A
// dispatcher may deliver asynchronously, so the suite polls rather than
// assuming the outcome has landed by the time the report call returns.
var settleWait = 2 * time.Second

// duplicateWindow is how long the sink is watched AFTER the first outcome
// arrives, to see whether a second one follows. It is paid in full by every
// terminal-report subtest, so it is deliberately small — long enough to catch
// a dispatcher that double-delivers, short enough not to dominate the suite.
var duplicateWindow = 50 * time.Millisecond

// absenceWait bounds a fetch that must return NOTHING — a wrong topic, a job
// already locked elsewhere. Unlike fetchWait it cannot be generous: the whole
// assertion is that no job comes, so every such subtest pays it in full.
//
// It is therefore a real trade-off rather than a hang-breaker, and it is the
// one wait in this suite that can fail a CORRECT adapter: one slower than this
// returns empty for the wrong reason and passes by luck. An adapter over a
// remote queue should raise it — see the note on fetchWait.
var absenceWait = 200 * time.Millisecond

// tb is the slice of *testing.T the individual contract assertions use. It
// exists so the suite's OWN failure branches can be driven in-process by a
// recording fake: those branches only run against a broken implementation, and
// an assertion that is never executed is an assertion nobody has checked —
// an inverted comparison would silently pass every adapter it was meant to
// reject.
//
// Conformance still takes a real *testing.T, because subtests need one.
type tb interface {
	Helper()
	Cleanup(func())
	Fatal(args ...any)
	Fatalf(format string, args ...any)
	Skip(args ...any)
}

// Waits returns the suite's current time bounds, so a caller can widen them
// for a backend this suite's defaults would falsely reject.
//
// The defaults suit an in-process queue. An adapter over a remote one does
// not: a dispatcher with a 20-second long-poll is CORRECT and would fail here
// for reasons unrelated to the contract. The suite is published for exactly
// those adapters (NFR-3), so the bounds are theirs to set.
func Waits() WaitConfig { return currentWaits() }

// SetWaits widens or narrows the suite's time bounds for the current test
// binary, returning a function that restores them.
//
// It is process-global and NOT safe to call from a parallel test. Set it once,
// before Conformance.
func SetWaits(w WaitConfig) func() { return applyWaits(w) }

// WaitConfig is the suite's time bounds. A zero field keeps the current value.
type WaitConfig struct {
	// Fetch bounds a FetchAndLock that MUST return. A hang-breaker.
	Fetch time.Duration

	// Settle is how long a terminal report is given to reach the sink.
	Settle time.Duration

	// Absence bounds a fetch that must return NOTHING. Every such assertion
	// pays it in full, and it is the one bound that can fail a CORRECT but slow
	// adapter — one slower than this returns empty for the wrong reason.
	Absence time.Duration
}

// Factory builds a fresh, empty WorkerDispatcher under test. It is called once
// per subtest, so implementations must return isolated queues (for a shared
// backend: a unique topic prefix or a wiped namespace).
type Factory func(t *testing.T) tasks.WorkerDispatcher

// Conformance runs the WorkerDispatcher contract against factory-built
// dispatchers. Adapter tests are one-liners:
//
//	func TestConformance(t *testing.T) {
//		taskstest.Conformance(t, func(t *testing.T) tasks.WorkerDispatcher {
//			return localdispatcher.New(clocktest.New(time.Now()), time.Minute)
//		})
//	}
//
// A dispatcher that implements tasks.SinkBinder gets the terminal-report
// subtests too; one that does not skips them, since without a sink there is no
// way to observe where a report went.
func Conformance(t *testing.T, factory Factory) {
	t.Helper()

	if factory == nil {
		t.Fatal("Conformance: a nil Factory isn't allowed")
	}

	for name, test := range conformanceTests {
		t.Run(name, func(t *testing.T) { test(t, factory(t)) })
	}
}

// conformanceTests is the contract as a declarative table.
var conformanceTests = map[string]func(tb, tasks.WorkerDispatcher){
	"EnqueueThenFetchLocks":      testEnqueueThenFetchLocks,
	"FetchOnlyRequestedTopics":   testFetchOnlyRequestedTopics,
	"FetchWakesOnEnqueue":        testFetchWakesOnEnqueue,
	"FetchHonorsContextCancel":   testFetchHonorsContextCancel,
	"LockedJobNotRefetched":      testLockedJobNotRefetched,
	"EnqueueRejectsEmptyID":      testEnqueueRejectsEmptyID,
	"ExtendLockIsHolderOnly":     testExtendLockIsHolderOnly,
	"CompleteReachesSink":        testCompleteReachesSink,
	"ReportBpmnErrorReachesSink": testReportBpmnErrorReachesSink,
	"ReportStatusReachesSink":    testReportStatusReachesSink,
	"FailReachesSink":            testFailReachesSink,
	"ReportsAreHolderOnly":       testReportsAreHolderOnly,
}

// recordSink captures the outcomes a dispatcher delivers.
type recordSink struct {
	outcomes []*tasks.WorkerOutcome
	mu       sync.Mutex
}

func (s *recordSink) ReportJobCompletion(
	_ context.Context, o *tasks.WorkerOutcome,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.outcomes = append(s.outcomes, o)

	return nil
}

func (s *recordSink) all() []*tasks.WorkerOutcome {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]*tasks.WorkerOutcome{}, s.outcomes...)
}

// bindSink attaches a recording sink, skipping the subtest when the dispatcher
// has no sink seam — there is nothing to observe then, and failing would
// reject a dispatcher that legitimately routes completions another way.
func bindSink(t tb, d tasks.WorkerDispatcher) *recordSink {
	t.Helper()

	binder, ok := d.(tasks.SinkBinder)
	if !ok {
		t.Skip("the dispatcher implements no tasks.SinkBinder, so a terminal " +
			"report cannot be observed")
	}

	s := &recordSink{}
	binder.BindSink(s)

	return s
}

// awaitOutcome polls until one outcome lands, or fails.
func awaitOutcome(t tb, s *recordSink) *tasks.WorkerOutcome {
	t.Helper()

	deadline := time.Now().Add(settleWait)

	var first []*tasks.WorkerOutcome

	for time.Now().Before(deadline) {
		if first = s.all(); len(first) != 0 {
			break
		}

		time.Sleep(5 * time.Millisecond)
	}

	if len(first) == 0 {
		t.Fatalf("no outcome reached the sink within %v", settleWait)

		return nil
	}

	// Keep watching after the first outcome lands. Returning here would only
	// catch a duplicate that arrived in the same instant — a dispatcher
	// delivering the second one a few milliseconds later would look correct,
	// which is exactly the bug "exactly 1" exists to catch.
	time.Sleep(duplicateWindow)

	if oo := s.all(); len(oo) != 1 {
		t.Fatalf("%d outcomes delivered for one report, want exactly 1", len(oo))

		return nil
	}

	return first[0]
}

// enqueueAndLock queues one job on topic and locks it to worker w1.
func enqueueAndLock(
	t tb, d tasks.WorkerDispatcher, id tasks.JobID, topic tasks.Topic,
) tasks.LockedJob {
	t.Helper()

	ctx := context.Background()

	if err := d.Enqueue(ctx, tasks.Job{ID: id, Topic: topic}); err != nil {
		t.Fatalf("Enqueue(%q): %v", id, err)
	}

	fctx, cancel := context.WithTimeout(ctx, fetchWait)
	defer cancel()

	jobs, err := d.FetchAndLock(fctx, "w1", []tasks.Topic{topic}, time.Minute)
	if err != nil {
		t.Fatalf("FetchAndLock: %v", err)
	}

	if len(jobs) == 0 {
		t.Fatal("FetchAndLock returned no job for an enqueued topic")
	}

	return jobs[0]
}

func testEnqueueThenFetchLocks(t tb, d tasks.WorkerDispatcher) {
	got := enqueueAndLock(t, d, "j1", "charge")

	if got.ID != "j1" {
		t.Fatalf("fetched job %q, want j1", got.ID)
	}

	if got.WorkerID != "w1" {
		t.Fatalf("job locked to %q, want w1 — the fetch must record its "+
			"holder, or no later report can be checked against it",
			got.WorkerID)
	}
}

// testFetchOnlyRequestedTopics: a worker subscribed to one topic must not
// receive another's job, or workers steal each other's work.
func testFetchOnlyRequestedTopics(t tb, d tasks.WorkerDispatcher) {
	ctx := context.Background()

	if err := d.Enqueue(ctx, tasks.Job{ID: "j1", Topic: "other"}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	fctx, cancel := context.WithTimeout(ctx, absenceWait)
	defer cancel()

	//nolint:errcheck // an empty fetch may report either way; the count is the test
	jobs, _ := d.FetchAndLock(fctx, "w1", []tasks.Topic{"charge"}, time.Minute)
	if len(jobs) != 0 {
		t.Fatalf("fetch on topic charge returned %d job(s) from topic other",
			len(jobs))
	}
}

// testFetchWakesOnEnqueue: FetchAndLock blocks until a job is available, so a
// fetcher parked on an empty topic must be woken by a later Enqueue rather
// than waiting out its context.
func testFetchWakesOnEnqueue(t tb, d tasks.WorkerDispatcher) {
	ctx, cancel := context.WithTimeout(context.Background(), fetchWait)
	defer cancel()

	type result struct {
		err  error
		jobs []tasks.LockedJob
	}

	done := make(chan result, 1)
	entered := make(chan struct{})

	go func() {
		close(entered)

		jobs, err := d.FetchAndLock(
			ctx, "w1", []tasks.Topic{"charge"}, time.Minute)
		done <- result{err: err, jobs: jobs}
	}()

	// Wait for the goroutine to reach the call, then confirm it has NOT
	// returned before enqueueing. A bare sleep proved nothing: if Enqueue won
	// the race the fetch found the job already waiting, and the test passed
	// without ever exercising the arrives-later path it exists for.
	<-entered

	select {
	case r := <-done:
		t.Fatalf("FetchAndLock returned before anything was enqueued "+
			"(%d job(s), err %v) — it must block until work exists",
			len(r.jobs), r.err)
	case <-time.After(absenceWait):
	}

	if err := d.Enqueue(
		context.Background(), tasks.Job{ID: "j1", Topic: "charge"},
	); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("FetchAndLock: %v", r.err)
		}

		if len(r.jobs) == 0 {
			t.Fatal("the woken fetch returned no job")
		}

	case <-time.After(fetchWait):
		t.Fatal("a blocked FetchAndLock was not woken by Enqueue")
	}
}

// testFetchHonorsContextCancel: a fetch on an empty queue must return when its
// context ends. A dispatcher that ignores it strands the worker goroutine on
// shutdown.
func testFetchHonorsContextCancel(t tb, d tasks.WorkerDispatcher) {
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})

	entered := make(chan struct{})

	go func() {
		close(entered)

		//nolint:errcheck // the assertion is that it RETURNS, not what it returns
		_, _ = d.FetchAndLock(ctx, "w1", []tasks.Topic{"charge"}, time.Minute)
		close(done)
	}()

	// Same handshake as the wake test: confirm the fetch is genuinely parked
	// before canceling. Canceling first would let a dispatcher that checks
	// ctx only on ENTRY pass, which is the implementation this exists to catch.
	<-entered

	select {
	case <-done:
		t.Fatal("FetchAndLock returned before its context was canceled and " +
			"before any job existed")
	case <-time.After(absenceWait):
	}

	cancel()

	select {
	case <-done:
	case <-time.After(fetchWait):
		t.Fatal("FetchAndLock ignored its canceled context")
	}
}

// testLockedJobNotRefetched: a locked job is exclusive for its lock duration,
// or two workers run the same service task.
func testLockedJobNotRefetched(t tb, d tasks.WorkerDispatcher) {
	enqueueAndLock(t, d, "j1", "charge")

	ctx, cancel := context.WithTimeout(context.Background(), absenceWait)
	defer cancel()

	//nolint:errcheck // a refused fetch may report either way; the count is the test
	jobs, _ := d.FetchAndLock(ctx, "w2", []tasks.Topic{"charge"}, time.Minute)
	if len(jobs) != 0 {
		t.Fatal("a job locked to w1 was fetched again by w2 — two workers " +
			"would run the same task")
	}
}

func testEnqueueRejectsEmptyID(t tb, d tasks.WorkerDispatcher) {
	if err := d.Enqueue(
		context.Background(), tasks.Job{Topic: "charge"},
	); err == nil {
		t.Fatal("Enqueue must reject a job with no ID — nothing could later " +
			"complete it")
	}
}

// testExtendLockIsHolderOnly: only the holder may extend, or any worker can
// keep another's job locked.
func testExtendLockIsHolderOnly(t tb, d tasks.WorkerDispatcher) {
	enqueueAndLock(t, d, "j1", "charge")

	if err := d.ExtendLock(
		context.Background(), "j1", "w2", time.Minute,
	); err == nil {
		t.Fatal("ExtendLock by a non-holder must be rejected")
	}
}

func testCompleteReachesSink(t tb, d tasks.WorkerDispatcher) {
	s := bindSink(t, d)
	enqueueAndLock(t, d, "j1", "charge")

	if err := d.Complete(context.Background(), "j1", "w1", nil); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	o := awaitOutcome(t, s)
	if o.Kind() != tasks.OutcomeComplete {
		t.Fatalf("Complete delivered kind %v, want OutcomeComplete", o.Kind())
	}

	if o.JobID() != "j1" {
		t.Fatalf("outcome carries job %q, want j1", o.JobID())
	}
}

func testReportBpmnErrorReachesSink(t tb, d tasks.WorkerDispatcher) {
	s := bindSink(t, d)
	enqueueAndLock(t, d, "j1", "charge")

	if err := d.ReportBpmnError(
		context.Background(), "j1", "w1", "E_DECLINED", "card declined",
	); err != nil {
		t.Fatalf("ReportBpmnError: %v", err)
	}

	o := awaitOutcome(t, s)

	code, _ := o.BpmnError()
	if code != "E_DECLINED" {
		t.Fatalf("outcome carries code %q, want E_DECLINED — the engine "+
			"raises this code to match an Error boundary event", code)
	}
}

func testReportStatusReachesSink(t tb, d tasks.WorkerDispatcher) {
	s := bindSink(t, d)
	enqueueAndLock(t, d, "j1", "charge")

	if err := d.ReportStatus(
		context.Background(), "j1", "w1", values.NewVariable("PARTIAL"),
	); err != nil {
		t.Fatalf("ReportStatus: %v", err)
	}

	o := awaitOutcome(t, s)
	if o.Kind() != tasks.OutcomeStatus {
		t.Fatalf("ReportStatus delivered kind %v, want OutcomeStatus", o.Kind())
	}
}

func testFailReachesSink(t tb, d tasks.WorkerDispatcher) {
	s := bindSink(t, d)
	enqueueAndLock(t, d, "j1", "charge")

	if err := d.Fail(
		context.Background(), "j1", "w1", tasks.Fault{Cause: errors.New("upstream 503")},
	); err != nil {
		t.Fatalf("Fail: %v", err)
	}

	// The KIND is not asserted: a dispatcher may forward the raw Fault for the
	// engine's ErrorMapper to classify, or classify it itself. Both are
	// conformant — what matters is that the report is not swallowed.
	awaitOutcome(t, s)
}

// testReportsAreHolderOnly: a terminal report from a worker that does not hold
// the lock must be rejected, or a stale worker can complete a job another
// worker is actively running.
func testReportsAreHolderOnly(t tb, d tasks.WorkerDispatcher) {
	enqueueAndLock(t, d, "j1", "charge")

	if err := d.Complete(context.Background(), "j1", "w2", nil); err == nil {
		t.Fatal("Complete by a non-holder must be rejected")
	}
}

// currentWaits and applyWaits keep the tunables in one place, so the exported
// surface stays a value type and the package variables stay unexported.
func currentWaits() WaitConfig {
	return WaitConfig{Fetch: fetchWait, Settle: settleWait, Absence: absenceWait}
}

func applyWaits(w WaitConfig) func() {
	prev := currentWaits()

	if w.Fetch > 0 {
		fetchWait = w.Fetch
	}

	if w.Settle > 0 {
		settleWait = w.Settle
	}

	if w.Absence > 0 {
		absenceWait = w.Absence
	}

	return func() {
		fetchWait, settleWait, absenceWait = prev.Fetch, prev.Settle, prev.Absence
	}
}
