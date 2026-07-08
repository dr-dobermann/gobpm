package localdispatcher_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/clock/clocktest"
	"github.com/dr-dobermann/gobpm/pkg/model/expression"
	expengine "github.com/dr-dobermann/gobpm/pkg/model/expression/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/tasks"
	"github.com/dr-dobermann/gobpm/pkg/tasks/localdispatcher"
	"github.com/stretchr/testify/require"
)

// clockPokeMapper is an ErrorMapper that advances a fake clock while classifying
// (returning Technical). It lets a test expire the reporting worker's lock during
// the classify window, exercising the retry re-validation guard.
type clockPokeMapper struct {
	clk *clocktest.Clock
	by  time.Duration
}

func (m clockPokeMapper) Classify(
	context.Context, expression.Engine, tasks.Fault,
) (tasks.MappedOutcome, error) {
	m.clk.Advance(m.by)

	return tasks.Technical{}, nil
}

// TestTechnicalFaultRetriesViaReArm: a technical fault with retries left re-arms
// the job (no delivery, the track stays parked); once the backoff elapses it is
// re-fetchable, preserving the attempt count (SRD-038 FR-7).
func TestTechnicalFaultRetriesViaReArm(t *testing.T) {
	sink := &recordSink{}
	clk := clocktest.New(base)
	d := localdispatcher.New(clk, time.Hour)
	d.BindSink(sink)

	ctx := context.Background()
	// no ErrorMapper → a raw Fail is Technical; FixedDelay(3, 1s) retries attempts 1, 2.
	require.NoError(t, d.Enqueue(ctx, tasks.Job{ID: "j1", Topic: "charge",
		Policy: &tasks.Policy{RetryPolicy: tasks.FixedDelay(3, time.Second)}}))

	_, err := d.FetchAndLock(ctx, "w1", topics("charge"), time.Minute)
	require.NoError(t, err)
	require.NoError(t, d.Fail(ctx, "j1", "w1",
		tasks.Fault{Cause: errors.New("boom")}))
	require.Nil(t, sink.last(), "a retried fault is not delivered")

	// after the backoff the job is fetchable again (its attempt count preserved).
	clk.Advance(time.Second)
	jobs, err := d.FetchAndLock(ctx, "w1", topics("charge"), time.Minute)
	require.NoError(t, err)
	require.Equal(t, tasks.JobID("j1"), jobs[0].ID)
	require.Nil(t, sink.last(), "still no terminal delivered mid-retry")
}

// TestLocalDispatcherNotBeforeGate: a re-armed job is withheld until its backoff
// elapses, then a blocked fetcher wakes on the clock's timed signal (SRD-038 §3.5).
func TestLocalDispatcherNotBeforeGate(t *testing.T) {
	sink := &recordSink{}
	clk := clocktest.New(base)
	d := localdispatcher.New(clk, time.Hour)
	d.BindSink(sink)

	ctx := t.Context()
	require.NoError(t, d.Enqueue(ctx, tasks.Job{ID: "j1", Topic: "charge",
		Policy: &tasks.Policy{RetryPolicy: tasks.FixedDelay(3, time.Second)}}))

	_, err := d.FetchAndLock(ctx, "w1", topics("charge"), time.Minute)
	require.NoError(t, err)
	require.NoError(t, d.Fail(ctx, "j1", "w1",
		tasks.Fault{Cause: errors.New("boom")}))

	// a fetcher blocks on the backoff gate and wakes when the clock passes it.
	got := make(chan tasks.LockedJob, 1)
	errc := make(chan error, 1)

	go func() {
		jobs, e := d.FetchAndLock(ctx, "w2", topics("charge"), time.Minute)
		if e != nil {
			errc <- e

			return
		}

		got <- jobs[0]
	}()

	time.Sleep(30 * time.Millisecond) // let the fetcher reach the timed wait
	clk.Advance(time.Second)          // fire the backoff-gate timer

	select {
	case lj := <-got:
		require.Equal(t, tasks.JobID("j1"), lj.ID)
	case e := <-errc:
		t.Fatalf("fetch errored: %v", e)
	case <-time.After(2 * time.Second):
		t.Fatal("the gated job wasn't handed out after the backoff elapsed")
	}
}

// TestRetryExhaustedFaultsWithDiagnostic: once the RetryPolicy is exhausted, the
// dispatcher delivers a terminal technical fault carrying the cause (SRD-038 FR-8).
func TestRetryExhaustedFaultsWithDiagnostic(t *testing.T) {
	sink := &recordSink{}
	d := localdispatcher.New(clocktest.New(base), time.Hour)
	d.BindSink(sink)

	ctx := context.Background()
	// NoRetry → the first technical fault is immediately exhausted.
	require.NoError(t, d.Enqueue(ctx, tasks.Job{ID: "j1", Topic: "charge",
		Policy: &tasks.Policy{RetryPolicy: tasks.NoRetry()}}))

	_, err := d.FetchAndLock(ctx, "w1", topics("charge"), time.Minute)
	require.NoError(t, err)

	require.NoError(t, d.Fail(ctx, "j1", "w1",
		tasks.Fault{Code: "503", Cause: errors.New("upstream down")}))

	out := sink.last()
	require.NotNil(t, out)
	require.Equal(t, tasks.OutcomeFault, out.Kind())
	require.ErrorContains(t, out.Fault().Cause, "upstream down")
}

// TestBusinessOutcomeNotRetried: a fault the ErrorMapper classifies as a Business
// Error is delivered immediately — the RetryPolicy is never consulted, the job is
// not re-armed (SRD-038 NFR-4).
func TestBusinessOutcomeNotRetried(t *testing.T) {
	sink := &recordSink{}
	d := localdispatcher.New(clocktest.New(base), time.Hour)
	d.BindSink(sink)
	d.BindExpressionEngine(expengine.New())

	ctx := context.Background()
	require.NoError(t, d.Enqueue(ctx, tasks.Job{ID: "j1", Topic: "charge",
		Policy: &tasks.Policy{
			ErrorMapper: mustRuleMapper(t,
				tasks.Rule{Code: "409", Yield: tasks.BpmnError{Code: "Conflict"}}),
			RetryPolicy: tasks.FixedDelay(3, time.Second)}}))

	_, err := d.FetchAndLock(ctx, "w1", topics("charge"), time.Minute)
	require.NoError(t, err)

	require.NoError(t, d.Fail(ctx, "j1", "w1", tasks.Fault{Code: "409"}))

	out := sink.last()
	require.NotNil(t, out, "a business verdict is delivered, not retried")
	require.Equal(t, tasks.OutcomeBpmnError, out.Kind())
}

// TestRetryReValidatesExpiredLock: if the reporting worker's lock expires during
// classification, the retry re-validation finds the lock expired and drops the
// re-arm rather than stomping a new holder (SRD-038 §3.4).
func TestRetryReValidatesExpiredLock(t *testing.T) {
	sink := &recordSink{}
	clk := clocktest.New(base)
	d := localdispatcher.New(clk, 10*time.Second)
	d.BindSink(sink)
	d.BindExpressionEngine(expengine.New())

	ctx := context.Background()
	require.NoError(t, d.Enqueue(ctx, tasks.Job{ID: "j1", Topic: "charge",
		Policy: &tasks.Policy{
			// classifying advances the clock 20s > the 10s lock — expiring it.
			ErrorMapper: clockPokeMapper{clk: clk, by: 20 * time.Second},
			RetryPolicy: tasks.FixedDelay(3, time.Second)}}))

	_, err := d.FetchAndLock(ctx, "w1", topics("charge"), 10*time.Second)
	require.NoError(t, err)

	err = d.Fail(ctx, "j1", "w1", tasks.Fault{Cause: errors.New("boom")})
	require.ErrorIs(t, err, localdispatcher.ErrLockExpired)
}
