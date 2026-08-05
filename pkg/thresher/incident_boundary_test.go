package thresher_test

// SRD-079 M6 T-11 — boundaries across an incident (ADR-036 §2.4): an armed
// boundary stays armed while the incident is open (an SLA timer keeps ticking
// against the stuck node), a retry never re-arms it, and an INTERRUPTING fire
// on the incident's node closes it as overtaken.

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// hourRetry keeps the incident's instance resident on a far-away schedule, so
// hub deliveries land on a live loop.
type hourRetry struct{}

func (hourRetry) Retry(int, error) (time.Duration, bool) {
	return time.Hour, true
}

// durExpr yields a constant duration for a timer boundary.
func durExpr(t *testing.T, id string, d time.Duration) data.FormalExpression {
	t.Helper()

	e, err := goexpr.New(nil,
		data.MustItemDefinition(values.NewVariable(time.Duration(0))),
		func(_ context.Context, _ data.Source) (data.Value, error) {
			return values.NewVariable(d), nil
		}, foundation.WithID(id))
	require.NoError(t, err)

	return e
}

// countOpN returns an operation adding one to n per run.
func countOpN(t *testing.T, name string, n *atomic.Int32) service.Operation {
	t.Helper()

	op, err := gooper.New(name,
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition,
		) (*data.ItemDefinition, error) {
			n.Add(1)

			return nil, nil
		})
	require.NoError(t, err)

	return op
}

// timerGuardedIncident builds start → host(op, hourRetry policy, one timer
// boundary firing after d) → end, with the boundary's flow running marker →
// its own end. interrupting selects the boundary kind.
func timerGuardedIncident(
	t *testing.T,
	procID string,
	op service.Operation,
	d time.Duration,
	interrupting bool,
	marker *atomic.Int32,
) *thresher.InstanceHandle {
	t.Helper()
	require.NoError(t, data.CreateDefaultStates())

	proc, err := process.New(procID)
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	host, err := activities.NewServiceTask("host", op,
		activities.WithoutParams(),
		activities.WithIncidentRetryPolicy(hourRetry{}))
	require.NoError(t, err)

	def, err := events.NewTimerEventDefinition(nil, nil,
		durExpr(t, procID+"-after", d))
	require.NoError(t, err)

	bnd, err := events.NewBoundaryEvent("sla", host, def, interrupting)
	require.NoError(t, err)

	slaTask, err := activities.NewServiceTask("sla-marker",
		countOpN(t, procID+"-marker", marker), activities.WithoutParams())
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	slaEnd, err := events.NewEndEvent("sla-end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, host, bnd, slaTask, end, slaEnd} {
		require.NoError(t, proc.Add(e))
	}

	link(t, start, host)
	link(t, host, end)
	link(t, bnd, slaTask)
	link(t, slaTask, slaEnd)

	th, err := thresher.New("test-"+procID, thresher.WithoutBanner())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, th.Run(ctx))

	_, err = th.RegisterProcess(proc)
	require.NoError(t, err)

	h, err := th.StartLatest(proc.ID())
	require.NoError(t, err)

	require.Eventually(t, func() bool { return h.OpenIncidents() == 1 },
		5*time.Second, 5*time.Millisecond)

	return h
}

// T-11a: a NON-INTERRUPTING timer boundary keeps ticking against the stuck
// node — it fires while the incident is open — and the operator's retry does
// not re-arm it: the marker runs exactly once across fail → fire → retry →
// completion.
func TestBoundaryFiresAcrossOpenIncident(t *testing.T) {
	var marker, calls atomic.Int32

	op, err := gooper.New("fail-once",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition,
		) (*data.ItemDefinition, error) {
			if calls.Add(1) == 1 {
				return nil, fmt.Errorf("first attempt fails")
			}

			return nil, nil
		})
	require.NoError(t, err)

	h := timerGuardedIncident(t, "inc-sla", op,
		150*time.Millisecond, false, &marker)

	// the SLA fires against the OPEN incident.
	require.Eventually(t, func() bool { return marker.Load() == 1 },
		5*time.Second, 5*time.Millisecond)
	require.Equal(t, 1, h.OpenIncidents(),
		"a non-interrupting fire leaves the incident open")

	// the retry heals the host; the boundary must NOT have re-armed.
	require.NoError(t,
		h.RetryIncident(context.Background(), h.Incidents()[0].ID))
	require.Eventually(t,
		func() bool { return h.State() == thresher.StateCompleted },
		5*time.Second, 5*time.Millisecond)

	time.Sleep(300 * time.Millisecond) // a re-armed timer would fire again
	require.EqualValues(t, 1, marker.Load(),
		"failing and retrying must never reset the SLA clock")
}

// T-11b: an INTERRUPTING boundary firing on the incident-holding node closes
// the incident as overtaken — the model made the operator's decision — and
// the exception flow completes the instance.
func TestInterruptingBoundaryOvertakesIncident(t *testing.T) {
	var marker atomic.Int32

	op, err := gooper.New("always-fail",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition,
		) (*data.ItemDefinition, error) {
			return nil, fmt.Errorf("always fails")
		})
	require.NoError(t, err)

	h := timerGuardedIncident(t, "inc-overtake", op,
		150*time.Millisecond, true, &marker)

	require.Eventually(t,
		func() bool { return h.State() == thresher.StateCompleted },
		5*time.Second, 5*time.Millisecond)

	require.EqualValues(t, 1, marker.Load(), "the exception flow ran")

	incs := h.Incidents()
	require.Len(t, incs, 1)
	require.Equal(t, "overtaken", incs[0].State)
	require.Zero(t, h.OpenIncidents())
}

