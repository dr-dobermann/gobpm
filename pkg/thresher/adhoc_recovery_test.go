package thresher_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
	"github.com/dr-dobermann/gobpm/pkg/adhoc"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/repository"
	"github.com/dr-dobermann/gobpm/pkg/repository/memrepo"
)

// SRD-083 T-7 — the worked trace end-to-end over a real Repository: an
// Ad-Hoc container killed mid-flight recovers on another engine at its
// recorded position.

// scriptRouter answers from a queue of turns and records every State
// it saw — the pkg-level twin of the internal scriptedRouter.
type scriptRouter struct {
	mu    sync.Mutex
	turns [][]string
	seen  []adhoc.State
}

func (r *scriptRouter) Next(
	_ context.Context, s adhoc.State,
) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.seen = append(r.seen, s)

	if len(r.turns) == 0 {
		return nil, nil
	}

	next := r.turns[0]
	r.turns = r.turns[1:]

	return next, nil
}

func (r *scriptRouter) states() []adhoc.State {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]adhoc.State(nil), r.seen...)
}

// adHocRecoveryProc builds start → triage(ad-hoc: a, b) → end with
// every node id PINNED (stable element identity across engines — the
// recovery requirement): "a" counts its runs, "b" parks on the gated
// conditional catch.
func adHocRecoveryProc(
	t *testing.T, key string, r adhoc.Router, gate *atomic.Int32,
	count *atomic.Int32,
) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New(key, foundation.WithID(key))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start",
		foundation.WithID(key+"-start"))
	require.NoError(t, err)

	op, err := gooper.New(key+"-op",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			count.Add(1)

			return nil, nil
		})
	require.NoError(t, err)

	triage, err := activities.NewSubProcess("triage",
		activities.WithAdHoc(r), foundation.WithID(key+"-triage"))
	require.NoError(t, err)

	a, err := activities.NewServiceTask("a", op,
		activities.WithoutParams(), foundation.WithID(key+"-a"))
	require.NoError(t, err)
	require.NoError(t, triage.Add(a))

	b, err := activities.NewSubProcess("b", foundation.WithID(key+"-b"))
	require.NoError(t, err)

	bStart, err := events.NewStartEvent("b-start",
		foundation.WithID(key+"-b-start"))
	require.NoError(t, err)

	def, err := events.NewConditionalEventDefinition(
		gateCondExpr(t, gate), foundation.WithID(key+"-b-cond"))
	require.NoError(t, err)

	catch, err := events.NewIntermediateCatchEvent("b-catch", def,
		foundation.WithID(key+"-b-catch"))
	require.NoError(t, err)

	bEnd, err := events.NewEndEvent("b-end",
		foundation.WithID(key+"-b-end"))
	require.NoError(t, err)

	for _, e := range []flow.Element{bStart, catch, bEnd} {
		require.NoError(t, b.Add(e))
	}

	link(t, bStart, catch)
	link(t, catch, bEnd)
	require.NoError(t, triage.Add(b))

	end, err := events.NewEndEvent("end", foundation.WithID(key+"-end"))
	require.NoError(t, err)

	for _, e := range []flow.Element{start, triage, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, triage)
	link(t, triage, end)

	return p
}

// TestAdHocKillAndResume: engine-1 runs the container until "a" has
// settled and "b" is parked, then is abandoned; engine-2 claims the
// record, restores the container at its position and completes it —
// "a" never re-runs, and the post-restore Router ask sees the true
// cross-crash progress.
func TestAdHocKillAndResume(t *testing.T) {
	const key = "ah-rec"

	repo := memrepo.New()

	var gate1, count1, gate2, count2 atomic.Int32

	r1 := &scriptRouter{turns: [][]string{{key + "-a"}, {key + "-b"}}}
	p1 := adHocRecoveryProc(t, key, r1, &gate1, &count1)

	th1, _, cancel1 := bootEngine(t, "engine-1", repo,
		80*time.Millisecond, p1)
	defer cancel1()

	h, err := th1.StartLatest(p1.ID())
	require.NoError(t, err)

	instID := h.ID()

	// wait until the durable record holds the worked-trace position:
	// "a" completed, "b" routed and parked.
	require.Eventually(t, func() bool {
		rec, ok, _ := repo.Load(context.Background(), instID)
		if !ok {
			return false
		}

		doc, err := checkpoint.Unmarshal(rec.Payload)
		if err != nil || len(doc.AdHoc) == 0 {
			return false
		}

		if doc.AdHoc[0].Completed[key+"-a"] != 1 {
			return false
		}

		for _, tr := range doc.Tracks {
			if tr.AdHocActivity == key+"-b" {
				return true
			}
		}

		return false
	}, 3*time.Second, 5*time.Millisecond,
		"the checkpoint must reach the worked-trace position")

	require.Equal(t, int32(1), count1.Load())

	time.Sleep(120 * time.Millisecond) // > engine-1's lease TTL

	gate2.Store(1) // engine-2's container can drain at once

	r2 := &scriptRouter{}
	p2 := adHocRecoveryProc(t, key, r2, &gate2, &count2)

	_, fw2, cancel2 := bootEngine(t, "engine-2", repo, time.Minute, p2)
	defer cancel2()

	require.Eventually(t, func() bool {
		return fw2.saw(observability.KindInstanceState,
			observability.PhaseRecovered)
	}, 2*time.Second, 5*time.Millisecond,
		"engine-2 must claim and recover the abandoned instance")

	require.Eventually(t, func() bool {
		rec, ok, _ := repo.Load(context.Background(), instID)

		return ok && rec.Status == repository.StatusCompleted &&
			rec.Lease.Owner == "engine-2"
	}, 3*time.Second, 10*time.Millisecond,
		"the recovered container must complete on engine-2")

	require.Equal(t, int32(0), count2.Load(),
		"the completed activity must not re-run across the crash")

	states := r2.states()
	require.NotEmpty(t, states,
		"the settle of the restored activity consults the Router")

	last := states[len(states)-1]
	require.Equal(t,
		map[string]int{key + "-a": 1, key + "-b": 1}, last.Completed,
		"the Router sees the true cross-crash progress")
	require.Equal(t, key+"-b", last.Last)
}
