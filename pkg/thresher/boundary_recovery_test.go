package thresher_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/repository"
	"github.com/dr-dobermann/gobpm/pkg/repository/memrepo"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// boundedTaskProc builds start → UserTask(guarded by an interrupting boundary
// timer at `when`) → end, with the boundary's exception flow recording that it
// fired. Every id is pinned: cross-engine recovery resolves the recorded ids
// against the recovering engine's own registration.
func boundedTaskProc(
	t *testing.T, key string, when time.Time, escalated *atomic.Bool,
) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New(key, foundation.WithID(key))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start",
		foundation.WithID(key+"-start"))
	require.NoError(t, err)

	ut, err := activities.NewUserTask("approve",
		activities.WithCandidateUsers("operator"),
		activities.WithOutput("result", "string", true),
		activities.WithoutParams(),
		foundation.WithID(key+"-approve"))
	require.NoError(t, err)

	def, err := events.NewTimerEventDefinition(timeExpr(t, when), nil, nil)
	require.NoError(t, err)

	// the boundary is a NODE: its id is pinned like every other, or the
	// recovering engine cannot resolve what the checkpoint recorded.
	bnd, err := events.NewBoundaryEvent("escalate", ut, def, true,
		foundation.WithID(key+"-escalate"))
	require.NoError(t, err)

	lane := pinnedLane(t, key+"-lane", escalated)

	end, err := events.NewEndEvent("end", foundation.WithID(key+"-end"))
	require.NoError(t, err)

	escEnd, err := events.NewEndEvent("esc-end",
		foundation.WithID(key+"-escend"))
	require.NoError(t, err)

	for _, e := range []flow.Element{start, ut, bnd, lane, end, escEnd} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, ut)
	link(t, ut, end)
	link(t, bnd, lane)
	link(t, lane, escEnd)

	return p
}

// TestRestartRecoveryBoundaryDeadline covers SRD-071 T-15 (FR-9a) end to end:
// an armed boundary survives a crash and fires at the deadline it was ORIGINALLY
// given, not one recomputed at recovery.
//
// "Approve within X or escalate" is the canonical shape, and before FR-9a the
// checkpoint recorded nothing about the boundary at all: the recovering engine
// re-armed it by re-evaluating the definition, so the escalation clock restarted
// on every recovery. An instance recovered often enough would never escalate —
// silently, since each individual recovery looks correct.
//
// The two engines build the SAME process with DIFFERENT deadlines — a near one
// on engine-1, an hour out on engine-2. That is what a duration-based boundary
// does in reality: "24h from now" resolves to a different instant every time it
// is evaluated, and the recovering engine evaluates it later. So the deadline
// engine-2 would compute for itself is an hour away, and the only way the
// escalation can fire inside this test is if the RECORDED one was restored.
func TestRestartRecoveryBoundaryDeadline(t *testing.T) {
	repo := memrepo.New()
	dist1 := &annCollector{}
	dist2 := &annCollector{}

	// Far enough out that engine-1 cannot fire the boundary — and complete the
	// instance, leaving engine-2 nothing in flight to recover — before the
	// crash-and-recover sequence finishes. Engine-1 is abandoned by lease, not
	// stopped, so it keeps running and its own timer stays armed; at 700ms this
	// test lost that race about once in a full parallel `go test ./...` sweep.
	// The margin costs nothing in assertion strength: an overdue RESTORED
	// deadline fires immediately (TestRestartRecoveryOverdueTimer pins that),
	// so the test proves the same thing whether the deadline is still ahead at
	// recovery time or already behind.
	deadline := time.Now().Add(2500 * time.Millisecond)

	var esc1, esc2 atomic.Bool

	p1 := boundedTaskProc(t, "rr-bnd", deadline, &esc1)

	th1 := bndEngine(t, "engine-1", repo, 80*time.Millisecond, dist1, p1)

	h, err := th1.StartLatest(p1.ID())
	require.NoError(t, err)

	instID := h.ID()

	require.Eventually(t, func() bool { return dist1.count() == 1 },
		2*time.Second, 5*time.Millisecond,
		"the guarded task must park and be announced")

	// The park checkpoint must carry the ARMED BOUNDARY before the crash — it
	// is what holds the recorded deadline. Gating on Status alone raced the
	// checkpoint that adds the boundary, and the assertions below would then
	// fail on a record that was merely young (the same gap waitParkedRecord
	// closes for the track's own wait).
	require.Eventually(t, func() bool {
		rec, ok, _ := repo.Load(context.Background(), instID)
		if !ok || rec.Status != repository.StatusActive {
			return false
		}

		d, uerr := checkpoint.Unmarshal(rec.Payload)

		return uerr == nil && len(d.Boundaries) > 0 && d.Boundaries[0].Timer != nil
	}, 2*time.Second, 5*time.Millisecond,
		"the park checkpoint must record the armed boundary")

	rec, ok, err := repo.Load(context.Background(), instID)
	require.NoError(t, err)
	require.True(t, ok)

	doc, err := checkpoint.Unmarshal(rec.Payload)
	require.NoError(t, err)
	require.NotEmpty(t, doc.Boundaries,
		"the checkpoint must record the armed boundary")
	require.NotNil(t, doc.Boundaries[0].Timer,
		"a timer boundary records the deadline it resolved")
	require.True(t, deadline.Equal(doc.Boundaries[0].Timer.Deadline),
		"the recorded deadline is the one the boundary was armed with")

	// crash by abandonment: let engine-1's lease lapse.
	time.Sleep(120 * time.Millisecond)

	// engine-2's own definition resolves an HOUR out: if the recovery
	// re-evaluated instead of restoring, nothing would fire here.
	p2 := boundedTaskProc(t, "rr-bnd", time.Now().Add(time.Hour), &esc2)

	bndEngine(t, "engine-2", repo, time.Minute, dist2, p2)

	require.Eventually(t, func() bool {
		return esc2.Load()
	}, 6*time.Second, 5*time.Millisecond,
		"the recovered boundary must fire at its RECORDED deadline — a "+
			"re-evaluated one would have restarted the escalation clock")
}

// bndEngine boots a checkpoint-armed engine with a task distributor and the
// given lease TTL — the short TTL is how engine-1 "crashes" (abandonment).
func bndEngine(
	t *testing.T, name string, repo repository.Repository,
	ttl time.Duration, dist *annCollector, p *process.Process,
) *thresher.Thresher {
	t.Helper()

	th, err := thresher.New(name,
		thresher.WithoutBanner(),
		thresher.WithoutStartupConfig(),
		thresher.WithRepository(repo),
		thresher.WithTaskDistributor(dist),
		thresher.WithEngineGroup(recoveryGroup),
		thresher.WithLeaseTTL(ttl))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	_, err = th.RegisterProcess(p)
	require.NoError(t, err)
	require.NoError(t, th.Run(ctx))

	return th
}
