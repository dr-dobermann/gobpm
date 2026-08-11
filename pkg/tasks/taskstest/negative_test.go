package taskstest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/tasks"
)

// strandDelay is how long the ignoreCtx stub keeps its caller waiting past
// cancellation. It must outlast the shrunk fetchWait so the assertion fails,
// and must not be derived from it — see FetchAndLock.
const strandDelay = 2 * time.Second

// fakeTB records what an assertion did instead of failing the real test.
type fakeTB struct {
	msg      string
	cleanups []func()
	failed   bool
	skipped  bool
}

// fakeAbort is the sentinel a fakeTB panics with, so drive can tell an
// intentional abort from a genuine panic and re-raise the latter.
type fakeAbort struct{}

func (f *fakeTB) Helper() {}

func (f *fakeTB) Cleanup(fn func()) { f.cleanups = append(f.cleanups, fn) }

func (f *fakeTB) Fatal(args ...any) {
	f.failed, f.msg = true, fmt.Sprint(args...)

	panic(fakeAbort{})
}

func (f *fakeTB) Fatalf(format string, args ...any) {
	f.failed, f.msg = true, fmt.Sprintf(format, args...)

	panic(fakeAbort{})
}

func (f *fakeTB) Skip(args ...any) {
	f.skipped, f.msg = true, fmt.Sprint(args...)

	panic(fakeAbort{})
}

// drive runs one contract assertion against d and reports what it did.
func drive(
	test func(tb, tasks.WorkerDispatcher), d tasks.WorkerDispatcher,
) *fakeTB {
	f := &fakeTB{}

	func() {
		defer func() {
			if r := recover(); r != nil {
				if _, ok := r.(fakeAbort); !ok {
					panic(r)
				}
			}
		}()

		test(f, d)
	}()

	for _, fn := range f.cleanups {
		fn()
	}

	return f
}

// stubDispatcher is a configurable dispatcher: each field turns off one part
// of the contract, so a test names the violation it checks.
//
// The mutex is not decoration. A real WorkerDispatcher is fetched from one
// goroutine while another enqueues — testFetchWakesOnEnqueue does exactly
// that — so a stub without it races, which -race caught in the full sweep
// after the package passed on its own.
type stubDispatcher struct {
	sink        tasks.JobCompletionSink
	handOut     bool  // FetchAndLock returns the queued job
	relock      bool  // a locked job is handed out again
	ignoreTopic bool  // topic filtering is not applied
	ignoreCtx   bool  // FetchAndLock never returns
	laxEnqueue  bool  // an empty job ID is accepted
	laxHolder   bool  // a non-holder may extend or report
	deaf        bool  // terminal reports never reach the sink
	noWorkerID  bool  // the fetched job records no holder
	reportErr   error // every terminal report fails
	wrongKind   bool  // the sink is handed the wrong outcome kind
	wrongJobID  bool  // the outcome names a different job
	twice       bool  // one report delivers two outcomes
	queued      []tasks.Job
	mu          sync.Mutex
}

func (d *stubDispatcher) BindSink(s tasks.JobCompletionSink) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.sink = s
}

func (d *stubDispatcher) Enqueue(_ context.Context, j tasks.Job) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if j.ID == "" && !d.laxEnqueue {
		return errors.New("empty job id")
	}

	d.queued = append(d.queued, j)

	return nil
}

// take returns the head job for topics, honoring the stub's configured
// misbehavior, or false when there is nothing to hand out.
func (d *stubDispatcher) take(topics []tasks.Topic) (tasks.Job, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if len(d.queued) == 0 {
		return tasks.Job{}, false
	}

	j := d.queued[0]

	if !d.ignoreTopic && len(topics) != 0 && j.Topic != topics[0] {
		return tasks.Job{}, false
	}

	if !d.relock {
		d.queued = d.queued[1:]
	}

	return j, true
}

func (d *stubDispatcher) FetchAndLock(
	ctx context.Context,
	w tasks.WorkerID,
	topics []tasks.Topic,
	_ time.Duration,
) ([]tasks.LockedJob, error) {
	if d.ignoreCtx {
		// Returns, but long after its context ended — a dispatcher that
		// strands the worker goroutine on shutdown. It does return, so the
		// stub leaks nothing once the test finishes.
		//
		// The delay is a fixed constant rather than a multiple of fetchWait
		// on purpose: this goroutine OUTLIVES the assertion that spawned it,
		// so reading the wait that shrinkWaits restores at cleanup is a data
		// race — one -race caught after the package passed without it.
		<-ctx.Done()
		time.Sleep(strandDelay)

		return nil, ctx.Err()
	}

	if !d.handOut {
		<-ctx.Done()

		return nil, ctx.Err()
	}

	// Poll rather than read once: a fetch must be woken by a LATER enqueue,
	// so returning empty on the first look would fail a conforming stub.
	var (
		j  tasks.Job
		ok bool
	)

	for !ok {
		if j, ok = d.take(topics); ok {
			break
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}

	lj := tasks.LockedJob{Job: j, WorkerID: w}
	if d.noWorkerID {
		lj.WorkerID = ""
	}

	return []tasks.LockedJob{lj}, nil
}

func (d *stubDispatcher) ExtendLock(
	_ context.Context, _ tasks.JobID, w tasks.WorkerID, _ time.Duration,
) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if w != "w1" && !d.laxHolder {
		return errors.New("not the holder")
	}

	return nil
}

func (d *stubDispatcher) report(
	ctx context.Context, _ tasks.JobID, w tasks.WorkerID, o *tasks.WorkerOutcome,
) error {
	d.mu.Lock()
	lax, deaf, sink := d.laxHolder, d.deaf, d.sink
	d.mu.Unlock()

	if w != "w1" && !lax {
		return errors.New("not the holder")
	}

	d.mu.Lock()
	rerr, wrongID, twice := d.reportErr, d.wrongJobID, d.twice
	d.mu.Unlock()

	if rerr != nil {
		return rerr
	}

	if deaf || sink == nil {
		return nil
	}

	if wrongID {
		o = tasks.NewWorkerComplete("someone-elses-job", nil)
	}

	if twice {
		if err := sink.ReportJobCompletion(ctx, o); err != nil {
			return err
		}
	}

	return sink.ReportJobCompletion(ctx, o)
}

func (d *stubDispatcher) Complete(
	ctx context.Context,
	id tasks.JobID,
	w tasks.WorkerID,
	_ *data.ItemDefinition,
) error {
	d.mu.Lock()
	wrong := d.wrongKind
	d.mu.Unlock()

	if wrong {
		return d.report(ctx, id, w, tasks.NewWorkerFault(id, tasks.Fault{
			Cause: errors.New("misreported"),
		}))
	}

	return d.report(ctx, id, w, tasks.NewWorkerComplete(id, nil))
}

func (d *stubDispatcher) ReportBpmnError(
	ctx context.Context, id tasks.JobID, w tasks.WorkerID, code, msg string,
) error {
	d.mu.Lock()
	wrong := d.wrongKind
	d.mu.Unlock()

	if wrong {
		code = "E_SOMETHING_ELSE"
	}

	return d.report(ctx, id, w, tasks.NewWorkerBpmnError(id, code, msg))
}

func (d *stubDispatcher) ReportStatus(
	ctx context.Context, id tasks.JobID, w tasks.WorkerID, v data.Value,
) error {
	d.mu.Lock()
	wrong := d.wrongKind
	d.mu.Unlock()

	if wrong {
		return d.report(ctx, id, w, tasks.NewWorkerComplete(id, nil))
	}

	return d.report(ctx, id, w, tasks.NewWorkerStatus(id, v))
}

func (d *stubDispatcher) Fail(
	ctx context.Context, id tasks.JobID, w tasks.WorkerID, f tasks.Fault,
) error {
	return d.report(ctx, id, w, tasks.NewWorkerFault(id, f))
}

// good returns a dispatcher satisfying every assertion.
func good() *stubDispatcher {
	return &stubDispatcher{handOut: true}
}

// shrinkWaits cuts the suite's hang-breakers for the negative tests, which
// force failures they already know how to produce.
// It goes through the exported SetWaits rather than assigning the package
// variables directly, so the negative tests exercise the same knob an adapter
// author uses — a tunable nothing in the repo calls is a tunable nobody has
// checked. SetWaits is process-global and its restore is registered with
// t.Cleanup, which is why no test in this package may call t.Parallel.
func shrinkWaits(t *testing.T) {
	t.Helper()

	t.Cleanup(SetWaits(WaitConfig{
		Fetch:   150 * time.Millisecond,
		Settle:  100 * time.Millisecond,
		Absence: 30 * time.Millisecond,
	}))
}

// TestAssertionsRejectBrokenDispatchers drives each contract assertion against
// a dispatcher that violates precisely it (SRD-088 T-9, in-process half).
func TestAssertionsRejectBrokenDispatchers(t *testing.T) {
	shrinkWaits(t)

	for name, tc := range map[string]struct {
		test func(tb, tasks.WorkerDispatcher)
		disp func() tasks.WorkerDispatcher
		want string
	}{
		"a queue that hands out no work": {
			test: testEnqueueThenFetchLocks,
			disp: func() tasks.WorkerDispatcher { return &stubDispatcher{} },
			want: "FetchAndLock",
		},
		"a fetch that records no holder": {
			test: testEnqueueThenFetchLocks,
			disp: func() tasks.WorkerDispatcher {
				d := good()
				d.noWorkerID = true

				return d
			},
			want: "locked to",
		},
		"a fetch that ignores topics": {
			test: testFetchOnlyRequestedTopics,
			disp: func() tasks.WorkerDispatcher {
				d := good()
				d.ignoreTopic = true

				return d
			},
			want: "from topic other",
		},
		"a fetch that errors instead of waking": {
			test: testFetchWakesOnEnqueue,
			disp: func() tasks.WorkerDispatcher { return &stubDispatcher{} },
			want: "FetchAndLock",
		},
		"a fetch that strands its worker after cancel": {
			test: testFetchHonorsContextCancel,
			disp: func() tasks.WorkerDispatcher {
				d := good()
				d.ignoreCtx = true

				return d
			},
			want: "canceled context",
		},
		"a job handed to a second worker": {
			test: testLockedJobNotRefetched,
			disp: func() tasks.WorkerDispatcher {
				d := good()
				d.relock = true

				return d
			},
			want: "fetched again",
		},
		"an enqueue that accepts an empty id": {
			test: testEnqueueRejectsEmptyID,
			disp: func() tasks.WorkerDispatcher {
				d := good()
				d.laxEnqueue = true

				return d
			},
			want: "must reject",
		},
		"an extend by a non-holder": {
			test: testExtendLockIsHolderOnly,
			disp: func() tasks.WorkerDispatcher {
				d := good()
				d.laxHolder = true

				return d
			},
			want: "must be rejected",
		},
		"a complete by a non-holder": {
			test: testReportsAreHolderOnly,
			disp: func() tasks.WorkerDispatcher {
				d := good()
				d.laxHolder = true

				return d
			},
			want: "must be rejected",
		},
		"a completion the dispatcher refuses": {
			test: testCompleteReachesSink,
			disp: func() tasks.WorkerDispatcher {
				d := good()
				d.reportErr = errors.New("refused")

				return d
			},
			want: "Complete:",
		},
		"a completion delivered as the wrong kind": {
			test: testCompleteReachesSink,
			disp: func() tasks.WorkerDispatcher {
				d := good()
				d.wrongKind = true

				return d
			},
			want: "want OutcomeComplete",
		},
		"an outcome naming a different job": {
			test: testCompleteReachesSink,
			disp: func() tasks.WorkerDispatcher {
				d := good()
				d.wrongJobID = true

				return d
			},
			want: "carries job",
		},
		"one report delivering two outcomes": {
			test: testCompleteReachesSink,
			disp: func() tasks.WorkerDispatcher {
				d := good()
				d.twice = true

				return d
			},
			want: "want exactly 1",
		},
		"a bpmn error reported under a different code": {
			test: testReportBpmnErrorReachesSink,
			disp: func() tasks.WorkerDispatcher {
				d := good()
				d.wrongKind = true

				return d
			},
			want: "want E_DECLINED",
		},
		"a bpmn error the dispatcher refuses": {
			test: testReportBpmnErrorReachesSink,
			disp: func() tasks.WorkerDispatcher {
				d := good()
				d.reportErr = errors.New("refused")

				return d
			},
			want: "ReportBpmnError:",
		},
		"a status delivered as a completion": {
			test: testReportStatusReachesSink,
			disp: func() tasks.WorkerDispatcher {
				d := good()
				d.wrongKind = true

				return d
			},
			want: "want OutcomeStatus",
		},
		"a status the dispatcher refuses": {
			test: testReportStatusReachesSink,
			disp: func() tasks.WorkerDispatcher {
				d := good()
				d.reportErr = errors.New("refused")

				return d
			},
			want: "ReportStatus:",
		},
		"a fault the dispatcher refuses": {
			test: testFailReachesSink,
			disp: func() tasks.WorkerDispatcher {
				d := good()
				d.reportErr = errors.New("refused")

				return d
			},
			want: "Fail:",
		},
		"a completion that never reaches the sink": {
			test: testCompleteReachesSink,
			disp: func() tasks.WorkerDispatcher {
				d := good()
				d.deaf = true

				return d
			},
			want: "no outcome reached the sink",
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := drive(tc.test, tc.disp())

			if !got.failed {
				t.Fatalf("the assertion PASSED %s", name)
			}

			if !strings.Contains(got.msg, tc.want) {
				t.Fatalf("failed with %q, which does not mention %q",
					got.msg, tc.want)
			}
		})
	}
}

// noSinkDispatcher implements no SinkBinder, so terminal-report assertions
// have nothing to observe and must skip rather than fail. It embeds the stub
// by VALUE and re-declares BindSink with a shape that does not satisfy
// tasks.SinkBinder, which is how a real dispatcher routing completions its own
// way would look to the suite.
type noSinkDispatcher struct{ *stubDispatcher }

func (noSinkDispatcher) BindSink() {} // deliberately the wrong signature

// TestSinkAssertionsSkipWithoutABinder: a dispatcher that routes completions
// its own way is conformant, so the suite must skip rather than reject it.
func TestSinkAssertionsSkipWithoutABinder(t *testing.T) {
	shrinkWaits(t)

	got := drive(testCompleteReachesSink, noSinkDispatcher{good()})

	if !got.skipped {
		t.Fatal("a dispatcher with no SinkBinder must SKIP the sink " +
			"assertions, not fail them")
	}
}

// TestWaitsAreTunable covers the knob published for out-of-repo adapters
// (NFR-3) — see the messagingtest twin for why an untested tunable is a trap.
func TestWaitsAreTunable(t *testing.T) {
	before := Waits()

	restore := SetWaits(WaitConfig{
		Fetch:   42 * time.Second,
		Settle:  7 * time.Second,
		Absence: 3 * time.Second,
	})

	got := Waits()
	if got.Fetch != 42*time.Second || got.Settle != 7*time.Second ||
		got.Absence != 3*time.Second {
		t.Fatalf("SetWaits did not take effect: %+v", got)
	}

	restore()

	if back := Waits(); back != before {
		t.Fatalf("restore left %+v, want %+v", back, before)
	}

	defer SetWaits(WaitConfig{Fetch: 9 * time.Second})()

	if partial := Waits(); partial.Absence != before.Absence {
		t.Fatalf("a zero field overwrote Absence: %+v", partial)
	}
}

// impatientDispatcher returns from FetchAndLock immediately, with no job and
// no error — the dispatcher that does not block at all.
type impatientDispatcher struct{ *stubDispatcher }

func (impatientDispatcher) FetchAndLock(
	context.Context, tasks.WorkerID, []tasks.Topic, time.Duration,
) ([]tasks.LockedJob, error) {
	return nil, nil
}

// TestHandshakeCatchesANonBlockingFetch covers the branch the handshake added:
// a dispatcher whose FetchAndLock returns straight away is rejected.
//
// The previous version slept and hoped, so this implementation passed — it
// simply happened to return before the sleep elapsed, which reads exactly like
// a fetch that parked and was woken.
func TestHandshakeCatchesANonBlockingFetch(t *testing.T) {
	shrinkWaits(t)

	for name, test := range map[string]func(tb, tasks.WorkerDispatcher){
		"FetchWakesOnEnqueue":      testFetchWakesOnEnqueue,
		"FetchHonorsContextCancel": testFetchHonorsContextCancel,
	} {
		t.Run(name, func(t *testing.T) {
			got := drive(test, impatientDispatcher{good()})

			if !got.failed {
				t.Fatal("a fetch that never blocks must be rejected")
			}

			if !strings.Contains(got.msg, "returned before") {
				t.Fatalf("failed with %q, which does not name the early return",
					got.msg)
			}
		})
	}
}
