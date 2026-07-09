package localdispatcher_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/clock/clocktest"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	expengine "github.com/dr-dobermann/gobpm/pkg/model/expression/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/tasks"
	"github.com/dr-dobermann/gobpm/pkg/tasks/localdispatcher"
	"github.com/stretchr/testify/require"
)

// trustedPool builds a dispatcher (clk, maxLock), registers fn on topic "charge",
// enqueues job "j1" with policy, and returns the sink the pool delivers to.
func trustedPool(
	t *testing.T,
	clk *clocktest.Clock,
	maxLock time.Duration,
	policy *tasks.Policy,
	fn localdispatcher.WorkerFunc,
) (*recordSink, context.CancelFunc) {
	t.Helper()
	require.NoError(t, data.CreateDefaultStates())

	sink := &recordSink{}
	d := localdispatcher.New(clk, maxLock)
	d.BindSink(sink)
	d.BindExpressionEngine(expengine.New())

	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, d.RegisterWorker(ctx, "charge", fn))
	require.NoError(t, d.Enqueue(ctx,
		tasks.Job{ID: "j1", Topic: "charge", Policy: policy}))

	return sink, cancel
}

// waitVerdict blocks until the pool delivers a verdict.
func waitVerdict(t *testing.T, sink *recordSink) *tasks.WorkerOutcome {
	t.Helper()
	require.Eventually(t, func() bool { return sink.last() != nil },
		2*time.Second, 5*time.Millisecond)

	return sink.last()
}

// TestWorkerTrustedMapsAndCompletes: a trusted worker maps its output in-process
// and reports a completion carrying the shaped data (SRD-039 FR-7).
func TestWorkerTrustedMapsAndCompletes(t *testing.T) {
	body := data.MustItemDefinition(values.NewVariable("order-42"),
		foundation.WithID("body"))

	sink, cancel := trustedPool(t, clocktest.New(base), time.Hour,
		&tasks.Policy{Trust: tasks.WorkerTrusted, OutputMapping: []tasks.OutputRule{
			{Path: bodyPathExpr(t), Var: "orderId"}}},
		func(context.Context, tasks.LockedJob) (*data.ItemDefinition, error) {
			return body, nil
		})
	defer cancel()

	out := waitVerdict(t, sink)
	require.Equal(t, tasks.OutcomeComplete, out.Kind())
	require.Equal(t, "orderId", out.Output()[0].Name())
	require.Equal(t, "order-42", out.Output()[0].Value().Get(context.Background()))
}

// TestWorkerTrustedMapErrorFaults: a required output path the body doesn't satisfy
// is a terminal fault even on a successful fn (SRD-039 §3.5).
func TestWorkerTrustedMapErrorFaults(t *testing.T) {
	body := data.MustItemDefinition(values.NewVariable("x"),
		foundation.WithID("body"))

	sink, cancel := trustedPool(t, clocktest.New(base), time.Hour,
		&tasks.Policy{Trust: tasks.WorkerTrusted, OutputMapping: []tasks.OutputRule{
			{Path: missingPathExpr(t), Var: "v", Required: true}}},
		func(context.Context, tasks.LockedJob) (*data.ItemDefinition, error) {
			return body, nil
		})
	defer cancel()

	require.Equal(t, tasks.OutcomeFault, waitVerdict(t, sink).Kind())
}

// TestWorkerTrustedReportsBpmnError: a *WorkerError with a BpmnErrorCode is the
// worker's self-classified Business Error verdict (SRD-039 FR-6/FR-7).
func TestWorkerTrustedReportsBpmnError(t *testing.T) {
	sink, cancel := trustedPool(t, clocktest.New(base), time.Hour,
		&tasks.Policy{Trust: tasks.WorkerTrusted},
		func(context.Context, tasks.LockedJob) (*data.ItemDefinition, error) {
			return nil, &tasks.WorkerError{BpmnErrorCode: "Conflict", Message: "dup"}
		})
	defer cancel()

	out := waitVerdict(t, sink)
	require.Equal(t, tasks.OutcomeBpmnError, out.Kind())
	code, msg := out.BpmnError()
	require.Equal(t, "Conflict", code)
	require.Equal(t, "dup", msg)
}

// TestWorkerTrustedReportsStatus: a *WorkerError with a Status is the worker's
// self-classified Business Status verdict.
func TestWorkerTrustedReportsStatus(t *testing.T) {
	sink, cancel := trustedPool(t, clocktest.New(base), time.Hour,
		&tasks.Policy{Trust: tasks.WorkerTrusted},
		func(context.Context, tasks.LockedJob) (*data.ItemDefinition, error) {
			return nil, &tasks.WorkerError{Status: values.NewVariable("NOT_FOUND")}
		})
	defer cancel()

	out := waitVerdict(t, sink)
	require.Equal(t, tasks.OutcomeStatus, out.Kind())
	require.NotNil(t, out.StatusValue())
}

// TestWorkerTrustedFallbackErrorMapper: a plain (non-*WorkerError) error runs the
// fallback ErrorMapper, which reclassifies it (a catch-all rule) as a Business
// Error (SRD-039 FR-6).
func TestWorkerTrustedFallbackErrorMapper(t *testing.T) {
	sink, cancel := trustedPool(t, clocktest.New(base), time.Hour,
		&tasks.Policy{Trust: tasks.WorkerTrusted, ErrorMapper: mustRuleMapper(t,
			tasks.Rule{Yield: tasks.BpmnError{Code: "Fallback"}})},
		func(context.Context, tasks.LockedJob) (*data.ItemDefinition, error) {
			return nil, errors.New("boom")
		})
	defer cancel()

	out := waitVerdict(t, sink)
	require.Equal(t, tasks.OutcomeBpmnError, out.Kind())
	code, _ := out.BpmnError()
	require.Equal(t, "Fallback", code)
}

// TestWorkerTrustedExhaustionForwarded: a technical *WorkerError with NoRetry is a
// terminal fault on first attempt — not re-classified/re-enqueued (SRD-039 FR-8,
// NFR-2).
func TestWorkerTrustedExhaustionForwarded(t *testing.T) {
	sink, cancel := trustedPool(t, clocktest.New(base), time.Hour,
		&tasks.Policy{Trust: tasks.WorkerTrusted, RetryPolicy: tasks.NoRetry()},
		func(context.Context, tasks.LockedJob) (*data.ItemDefinition, error) {
			return nil, &tasks.WorkerError{Cause: errors.New("upstream down")}
		})
	defer cancel()

	require.Equal(t, tasks.OutcomeFault, waitVerdict(t, sink).Kind())
}

// TestWorkerTrustedRetryExceedsLockWindow: a backoff that would outlast the held
// lock terminates the retry as exhausted, never lapsing into a re-fetch (NFR-2).
func TestWorkerTrustedRetryExceedsLockWindow(t *testing.T) {
	sink, cancel := trustedPool(t, clocktest.New(base), 10*time.Millisecond,
		&tasks.Policy{Trust: tasks.WorkerTrusted,
			RetryPolicy: tasks.FixedDelay(3, time.Second)},
		func(context.Context, tasks.LockedJob) (*data.ItemDefinition, error) {
			return nil, errors.New("boom")
		})
	defer cancel()

	require.Equal(t, tasks.OutcomeFault, waitVerdict(t, sink).Kind())
}

// TestWorkerTrustedRetriesInProcess: a trusted worker whose fn fails transiently
// then succeeds retries in-process (backoff on the injected clock, no re-enqueue)
// and completes (SRD-039 FR-7, NFR-5).
func TestWorkerTrustedRetriesInProcess(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	clk := clocktest.New(base)
	sink := &recordSink{}
	d := localdispatcher.New(clk, time.Hour)
	d.BindSink(sink)
	d.BindExpressionEngine(expengine.New())

	body := data.MustItemDefinition(values.NewVariable("ok"),
		foundation.WithID("body"))
	called := make(chan struct{}, 8)

	var n atomic.Int32

	fn := func(context.Context, tasks.LockedJob) (*data.ItemDefinition, error) {
		c := n.Add(1)
		called <- struct{}{}

		if c < 3 {
			return nil, errors.New("transient")
		}

		return body, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, d.RegisterWorker(ctx, "charge", fn))
	require.NoError(t, d.Enqueue(ctx, tasks.Job{ID: "j1", Topic: "charge",
		Policy: &tasks.Policy{
			Trust: tasks.WorkerTrusted, RetryPolicy: tasks.FixedDelay(3, time.Second)}}))

	// attempts 1 and 2 fail → the worker backs off in-process; release each.
	for range 2 {
		<-called
		time.Sleep(20 * time.Millisecond) // let runTrusted reach clk.After
		clk.Advance(time.Second)
	}

	<-called // attempt 3 (success)

	require.Equal(t, tasks.OutcomeComplete, waitVerdict(t, sink).Kind())
	require.Equal(t, int32(3), n.Load())
}

// TestWorkerTrustedCtxCancelDuringBackoff: cancelling the context while a trusted
// worker is backing off aborts the retry (the ctx.Done path); no verdict lands.
func TestWorkerTrustedCtxCancelDuringBackoff(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	clk := clocktest.New(base)
	sink := &recordSink{}
	d := localdispatcher.New(clk, time.Hour)
	d.BindSink(sink)
	d.BindExpressionEngine(expengine.New())

	called := make(chan struct{}, 4)
	fn := func(context.Context, tasks.LockedJob) (*data.ItemDefinition, error) {
		called <- struct{}{}

		return nil, errors.New("transient")
	}

	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, d.RegisterWorker(ctx, "charge", fn))
	require.NoError(t, d.Enqueue(ctx, tasks.Job{ID: "j1", Topic: "charge",
		Policy: &tasks.Policy{
			Trust: tasks.WorkerTrusted, RetryPolicy: tasks.FixedDelay(5, time.Second)}}))

	<-called                          // attempt 1 failed → backing off
	time.Sleep(20 * time.Millisecond) // let runTrusted reach clk.After
	cancel()                          // cancel during the backoff → runTrusted returns

	time.Sleep(50 * time.Millisecond)
	require.Nil(t, sink.last(), "no verdict is delivered after the retry is aborted")
}

// errSink is a JobCompletionSink whose delivery always fails.
type errSink struct{ called atomic.Bool }

func (s *errSink) ReportJobCompletion(
	context.Context, *tasks.WorkerOutcome,
) error {
	s.called.Store(true)

	return errors.New("sink boom")
}

// TestWorkerTrustedReportErrorLogged: a failed verdict delivery is logged, not
// dropped silently (reportTrusted's error path).
func TestWorkerTrustedReportErrorLogged(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	sink := &errSink{}
	d := localdispatcher.New(clocktest.New(base), time.Hour)
	d.BindSink(sink)
	d.BindExpressionEngine(expengine.New())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, d.RegisterWorker(ctx, "charge",
		func(context.Context, tasks.LockedJob) (*data.ItemDefinition, error) {
			return nil, &tasks.WorkerError{Cause: errors.New("boom")}
		}))
	require.NoError(t, d.Enqueue(ctx, tasks.Job{ID: "j1", Topic: "charge",
		Policy: &tasks.Policy{Trust: tasks.WorkerTrusted, RetryPolicy: tasks.NoRetry()}}))

	require.Eventually(t, sink.called.Load, 2*time.Second, 5*time.Millisecond)
}

// TestTrustSelectsPolicyLocus: the same self-classifying fn yields different
// outcomes per mode — WorkerTrusted honours the worker's verdict; EngineAuthoritative
// ignores it and the dispatcher classifies the raw fault (SRD-039 FR-5).
func TestTrustSelectsPolicyLocus(t *testing.T) {
	fn := func(context.Context, tasks.LockedJob) (*data.ItemDefinition, error) {
		return nil, &tasks.WorkerError{BpmnErrorCode: "FromWorker"}
	}

	trusted, c1 := trustedPool(t, clocktest.New(base), time.Hour,
		&tasks.Policy{Trust: tasks.WorkerTrusted}, fn)
	defer c1()
	require.Equal(t, tasks.OutcomeBpmnError, waitVerdict(t, trusted).Kind())

	engine, c2 := trustedPool(t, clocktest.New(base), time.Hour,
		&tasks.Policy{Trust: tasks.EngineAuthoritative, RetryPolicy: tasks.NoRetry()},
		fn)
	defer c2()
	require.Equal(t, tasks.OutcomeFault, waitVerdict(t, engine).Kind())
}
