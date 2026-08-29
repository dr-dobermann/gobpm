package instance

// SRD-079 M6 — the interaction contracts (ADR-036 §2.4) as tests: the
// compensation ledger records completions only (T-12), and a Multi-Instance
// incident belongs to one inner instance — never the set (T-13).

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
)

// failFirstOp fails only its very first call — across every instance sharing
// it — then succeeds; calls counts the total executions.
func failFirstOp(t *testing.T, calls *atomic.Int32) service.Operation {
	t.Helper()

	op, err := gooper.New("fail-first",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition,
		) (*data.ItemDefinition, error) {
			if calls.Add(1) == 1 {
				return nil, fmt.Errorf("first call fails")
			}

			return nil, nil
		})
	require.NoError(t, err)

	return op
}

// quickRetry is the tiny-backoff policy the contract tests heal with.
var quickRetry = capPolicy{backoff: 5 * time.Millisecond, max: 5}

// T-12: the compensation ledger holds exactly one entry for a
// failed-then-retried activity — failed attempts never ledger, so its
// compensation handler runs exactly once.
func TestLedgerExcludesFailedAttempts(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var calls, undone atomic.Int32

	p, err := process.New("inc-comp")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	a, err := activities.NewServiceTask("A", failFirstOp(t, &calls),
		activities.WithoutParams(),
		activities.WithIncidentRetryPolicy(quickRetry))
	require.NoError(t, err)

	undoOp, err := gooper.New("undo-A-op",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition,
		) (*data.ItemDefinition, error) {
			undone.Add(1)

			return nil, nil
		})
	require.NoError(t, err)

	undo, err := activities.NewServiceTask("undo-A", undoOp,
		activities.WithoutParams(), activities.WithCompensation())
	require.NoError(t, err)

	throw := compThrow(t, "throw", a, true)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range append([]flow.Element{start, a, throw, end},
		guardWith(t, a, undo)...) {
		require.NoError(t, p.Add(e))
	}
	linkAll(t, [2]flow.Element{start, a}, [2]flow.Element{a, throw},
		[2]flow.Element{throw, end})

	inst, _ := observeInstance(t, p)

	require.Equal(t, Completed, inst.State())
	require.EqualValues(t, 2, calls.Load(),
		"the failed attempt plus the healing retry")
	require.EqualValues(t, 1, undone.Load(),
		"one ledger entry — the handler runs once, never per attempt")

	views := inst.IncidentViews()
	require.Len(t, views, 1)
	require.Equal(t, "resolved", views[0].State)
}

// miIncidentInstance builds start → body(SubProcess + MI) → end where the
// inner work task carries the quick retry policy.
func miIncidentInstance(
	t *testing.T,
	op service.Operation,
	mi *activities.MultiInstanceLoopCharacteristics,
) *Instance {
	t.Helper()

	_ = data.CreateDefaultStates()

	p, err := process.New("mi-inc")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	body, err := activities.NewSubProcess("body", activities.WithLoop(mi))
	require.NoError(t, err)

	bStart, err := events.NewStartEvent("b-start")
	require.NoError(t, err)

	work, err := activities.NewServiceTask("work", op,
		activities.WithoutParams(),
		activities.WithIncidentRetryPolicy(quickRetry))
	require.NoError(t, err)

	bEnd, err := events.NewEndEvent("b-end")
	require.NoError(t, err)

	for _, e := range []flow.Element{bStart, work, bEnd} {
		require.NoError(t, body.Add(e))
	}

	_, err = flow.Link(bStart, work)
	require.NoError(t, err)
	_, err = flow.Link(work, bEnd)
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, body, end} {
		require.NoError(t, p.Add(e))
	}

	_, err = flow.Link(start, body)
	require.NoError(t, err)
	_, err = flow.Link(body, end)
	require.NoError(t, err)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(),
		&recordingProducer{}, nil)
	require.NoError(t, err)

	return inst
}

// T-13: one inner instance's failure raises ONE incident, the retry re-runs
// only that instance, and the set completes — in both MI shapes. In the
// sequential shape the failure blocks the remainder of the sequence until the
// retry heals, which the completion itself proves.
func TestMIIncidentPerInstance(t *testing.T) {
	for _, sh := range miShapes() {
		t.Run(sh.name, func(t *testing.T) {
			var calls atomic.Int32

			mi := sh.mk(t, activities.WithCardinality(cardExpr(t, 3)))
			inst := miIncidentInstance(t, failFirstOp(t, &calls), mi)

			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			require.NoError(t, inst.Run(ctx))

			require.Eventually(t,
				func() bool { return inst.State() == Completed },
				5*time.Second, 5*time.Millisecond)

			require.EqualValues(t, 4, calls.Load(),
				"three instances plus exactly one retry — "+
					"siblings are never re-run")

			views := inst.IncidentViews()
			require.Len(t, views, 1,
				"one incident, on the failed inner instance alone")
			require.Equal(t, "resolved", views[0].State)
			require.Equal(t, 1, views[0].Attempts)
		})
	}
}
