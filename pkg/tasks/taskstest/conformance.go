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
const fetchWait = 5 * time.Second

// settleWait is how long a terminal report is given to reach the sink. A
// dispatcher may deliver asynchronously, so the suite polls rather than
// assuming the outcome has landed by the time the report call returns.
const settleWait = 2 * time.Second

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
var conformanceTests = map[string]func(*testing.T, tasks.WorkerDispatcher){
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
func bindSink(t *testing.T, d tasks.WorkerDispatcher) *recordSink {
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
func awaitOutcome(t *testing.T, s *recordSink) *tasks.WorkerOutcome {
	t.Helper()

	deadline := time.Now().Add(settleWait)

	for time.Now().Before(deadline) {
		if oo := s.all(); len(oo) != 0 {
			if len(oo) != 1 {
				t.Fatalf("%d outcomes delivered for one report, want exactly 1",
					len(oo))
			}

			return oo[0]
		}

		time.Sleep(5 * time.Millisecond)
	}

	t.Fatalf("no outcome reached the sink within %v", settleWait)

	return nil
}

// enqueueAndLock queues one job on topic and locks it to worker w1.
func enqueueAndLock(
	t *testing.T, d tasks.WorkerDispatcher, id tasks.JobID, topic tasks.Topic,
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

func testEnqueueThenFetchLocks(t *testing.T, d tasks.WorkerDispatcher) {
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
func testFetchOnlyRequestedTopics(t *testing.T, d tasks.WorkerDispatcher) {
	ctx := context.Background()

	if err := d.Enqueue(ctx, tasks.Job{ID: "j1", Topic: "other"}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	fctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
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
func testFetchWakesOnEnqueue(t *testing.T, d tasks.WorkerDispatcher) {
	ctx, cancel := context.WithTimeout(context.Background(), fetchWait)
	defer cancel()

	type result struct {
		err  error
		jobs []tasks.LockedJob
	}

	done := make(chan result, 1)

	go func() {
		jobs, err := d.FetchAndLock(
			ctx, "w1", []tasks.Topic{"charge"}, time.Minute)
		done <- result{err: err, jobs: jobs}
	}()

	// let the fetcher park before the job exists
	time.Sleep(50 * time.Millisecond)

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
func testFetchHonorsContextCancel(t *testing.T, d tasks.WorkerDispatcher) {
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})

	go func() {
		//nolint:errcheck // the assertion is that it RETURNS, not what it returns
		_, _ = d.FetchAndLock(ctx, "w1", []tasks.Topic{"charge"}, time.Minute)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(fetchWait):
		t.Fatal("FetchAndLock ignored its canceled context")
	}
}

// testLockedJobNotRefetched: a locked job is exclusive for its lock duration,
// or two workers run the same service task.
func testLockedJobNotRefetched(t *testing.T, d tasks.WorkerDispatcher) {
	enqueueAndLock(t, d, "j1", "charge")

	ctx, cancel := context.WithTimeout(
		context.Background(), 200*time.Millisecond)
	defer cancel()

	//nolint:errcheck // a refused fetch may report either way; the count is the test
	jobs, _ := d.FetchAndLock(ctx, "w2", []tasks.Topic{"charge"}, time.Minute)
	if len(jobs) != 0 {
		t.Fatal("a job locked to w1 was fetched again by w2 — two workers " +
			"would run the same task")
	}
}

func testEnqueueRejectsEmptyID(t *testing.T, d tasks.WorkerDispatcher) {
	if err := d.Enqueue(
		context.Background(), tasks.Job{Topic: "charge"},
	); err == nil {
		t.Fatal("Enqueue must reject a job with no ID — nothing could later " +
			"complete it")
	}
}

// testExtendLockIsHolderOnly: only the holder may extend, or any worker can
// keep another's job locked.
func testExtendLockIsHolderOnly(t *testing.T, d tasks.WorkerDispatcher) {
	enqueueAndLock(t, d, "j1", "charge")

	if err := d.ExtendLock(
		context.Background(), "j1", "w2", time.Minute,
	); err == nil {
		t.Fatal("ExtendLock by a non-holder must be rejected")
	}
}

func testCompleteReachesSink(t *testing.T, d tasks.WorkerDispatcher) {
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

func testReportBpmnErrorReachesSink(t *testing.T, d tasks.WorkerDispatcher) {
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

func testReportStatusReachesSink(t *testing.T, d tasks.WorkerDispatcher) {
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

func testFailReachesSink(t *testing.T, d tasks.WorkerDispatcher) {
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
func testReportsAreHolderOnly(t *testing.T, d tasks.WorkerDispatcher) {
	enqueueAndLock(t, d, "j1", "charge")

	if err := d.Complete(context.Background(), "j1", "w2", nil); err == nil {
		t.Fatal("Complete by a non-holder must be rejected")
	}
}
