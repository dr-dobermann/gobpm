package thresher_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/repository"
	"github.com/dr-dobermann/gobpm/pkg/repository/memrepo"
)

// compositeLongWaitProc builds start → Sub-Process(start → long timer → end)
// → lane → end: the host hosts a scope while the body's own token parks on a
// wait an engine holder takes.
func compositeLongWaitProc(
	t *testing.T, key string, deadline time.Time, hit *atomic.Bool,
) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New(key, foundation.WithID(key))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start",
		foundation.WithID(key+"-start"))
	require.NoError(t, err)

	body, err := activities.NewSubProcess("body",
		foundation.WithID(key+"-body"))
	require.NoError(t, err)

	def, err := events.NewTimerEventDefinition(timeExpr(t, deadline), nil, nil)
	require.NoError(t, err)

	bStart, err := events.NewStartEvent("b-start",
		foundation.WithID(key+"-b-start"))
	require.NoError(t, err)

	wait, err := events.NewIntermediateCatchEvent("b-wait", def,
		foundation.WithID(key+"-b-wait"))
	require.NoError(t, err)

	bEnd, err := events.NewEndEvent("b-end", foundation.WithID(key+"-b-end"))
	require.NoError(t, err)

	for _, e := range []flow.Element{bStart, wait, bEnd} {
		require.NoError(t, body.Add(e))
	}

	link(t, bStart, wait)
	link(t, wait, bEnd)

	// the lane AFTER the Sub-Process: it runs only if the host resumed, which
	// is what makes the round trip observable rather than merely survivable.
	lane := pinnedLane(t, key+"-lane", hit)

	end, err := events.NewEndEvent("end", foundation.WithID(key+"-end"))
	require.NoError(t, err)

	for _, e := range []flow.Element{start, body, lane, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, body)
	link(t, body, lane)
	link(t, lane, end)

	return p
}

// TestDehydratedSubProcessHostResumes (SRD-090.A M3d, FR-8 end to end): a
// Sub-Process whose BODY is parked on a held long wait releases its whole
// instance — host included — and comes back.
//
// The unit tests pin the decision; this pins the round trip, which is the
// half that can be wrong while every decision is right. The host's goroutine
// is released mid-activity: its scope is not closed, its body is not torn
// down, and on wake the restore has to re-enter the decorator at its recorded
// position and re-attach it to the scope it left. Nothing before M3d could
// exercise that, because a composite host never dehydrated.
//
// The flow passing the lane AFTER the Sub-Process is the assertion: it is
// reachable only if the host resumed and followed the activity's outgoing
// flow, not merely if the body finished.
func TestDehydratedSubProcessHostResumes(t *testing.T) {
	repo := memrepo.New()
	deadline := dehydrationEpoch.Add(2 * time.Hour) // > the 1h threshold

	var hit atomic.Bool

	p := compositeLongWaitProc(t, "dehy-composite", deadline, &hit)

	th, fw, clk, cancel := bootDehydrationEngine(t, "engine-SP", repo, p)
	defer cancel()

	h, err := th.StartLatest(p.ID())
	require.NoError(t, err)

	instID := h.ID()

	require.Eventually(t, func() bool {
		return fw.saw(observability.KindInstanceState,
			observability.PhaseDehydrated)
	}, 3*time.Second, 5*time.Millisecond,
		"the host holds no wait of its own, so the body's held timer decides")

	rec, ok, err := repo.Load(context.Background(), instID)
	require.NoError(t, err)
	require.True(t, ok, "the dehydration checkpoint is the wake source")
	require.Equal(t, repository.StatusActive, rec.Status)

	require.False(t, hit.Load(),
		"the flow must not have passed the Sub-Process yet")

	clk.Advance(30 * time.Minute)
	require.Never(t, hit.Load, 200*time.Millisecond, 20*time.Millisecond,
		"the held timer must not fire early")

	// the deadline arrives: the instance wakes, the body's token continues to
	// its end, the scope drains, and the HOST — re-entered at its recorded
	// position — follows the activity's outgoing flow.
	clk.Advance(2 * time.Hour)

	require.Eventually(t, hit.Load, 5*time.Second, 5*time.Millisecond,
		"the woken host must resume and follow the Sub-Process's outgoing flow")

	require.Eventually(t, func() bool {
		r, found, _ := repo.Load(context.Background(), instID)

		return found && r.Status == repository.StatusCompleted
	}, 5*time.Second, 10*time.Millisecond,
		"the woken instance must run to completion")
}

// iteratedLongWaitProc is compositeLongWaitProc with the Sub-Process made a
// SEQUENTIAL Multi-Instance of three: every iteration opens its own pass of
// the body and parks on the same held wait, so the host is released from
// inside a decorator rather than as a single executor.
func iteratedLongWaitProc(
	t *testing.T, key string, wait time.Duration, hit *atomic.Bool,
) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New(key, foundation.WithID(key))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start",
		foundation.WithID(key+"-start"))
	require.NoError(t, err)

	mi, err := activities.NewMultiInstance(
		activities.WithSequential(), activities.WithCardinality(mixCard(t, 3)))
	require.NoError(t, err)

	body, err := activities.NewSubProcess("body",
		activities.WithLoop(mi), foundation.WithID(key+"-body"))
	require.NoError(t, err)

	// a DURATION rather than an absolute date: every iteration arms this
	// node again, and a date already in the past is refused outright
	// ("couldn't use past time as a timer"). Each pass computes its own
	// deadline from the current clock, which is what a re-armed wait needs.
	def, err := events.NewTimerEventDefinition(nil, nil,
		durExpr(t, key+"-after", wait))
	require.NoError(t, err)

	bStart, err := events.NewStartEvent("b-start",
		foundation.WithID(key+"-b-start"))
	require.NoError(t, err)

	catch, err := events.NewIntermediateCatchEvent("b-wait", def,
		foundation.WithID(key+"-b-wait"))
	require.NoError(t, err)

	bEnd, err := events.NewEndEvent("b-end", foundation.WithID(key+"-b-end"))
	require.NoError(t, err)

	for _, e := range []flow.Element{bStart, catch, bEnd} {
		require.NoError(t, body.Add(e))
	}

	link(t, bStart, catch)
	link(t, catch, bEnd)

	lane := pinnedLane(t, key+"-lane", hit)

	end, err := events.NewEndEvent("end", foundation.WithID(key+"-end"))
	require.NoError(t, err)

	for _, e := range []flow.Element{start, body, lane, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, body)
	link(t, body, lane)
	link(t, lane, end)

	return p
}

// TestDehydratedIteratedSubProcessResumes (SRD-090.A M3d, T-6 literally): a
// SEQUENTIAL Multi-Instance Sub-Process whose every iteration parks on a held
// wait releases its instance and comes back — three times over, finishing all
// three iterations after the wake.
//
// The plain-composite round trip does not cover this. An iterated host is
// released from inside a DECORATOR: `iterDecorator.awaits()` delegates to the
// live instance, so the release unwinds through the decorator's pass loop
// rather than through a single executor, and the restore has to re-enter that
// decorator at the recorded ordinal rather than at a single open scope. That
// is the case ADR-007 v.2.1 names — an iterated Sub-Process holding parked
// work — so it is the one worth driving end to end.
func TestDehydratedIteratedSubProcessResumes(t *testing.T) {
	repo := memrepo.New()

	var hit atomic.Bool

	// > the 1h hold threshold, so every iteration's wait is externalized
	p := iteratedLongWaitProc(t, "dehy-mi-sp", 2*time.Hour, &hit)

	th, fw, clk, cancel := bootDehydrationEngine(t, "engine-MI", repo, p)
	defer cancel()

	h, err := th.StartLatest(p.ID())
	require.NoError(t, err)

	instID := h.ID()

	require.Eventually(t, func() bool {
		return fw.saw(observability.KindInstanceState,
			observability.PhaseDehydrated)
	}, 3*time.Second, 5*time.Millisecond,
		"the first iteration's held wait releases the whole instance")

	require.False(t, hit.Load(), "the flow must not have passed the activity")

	// Each wake finishes ONE iteration and parks on the next one's wait, so
	// the instance dehydrates and hydrates once per iteration — and each
	// iteration's deadline is computed from the clock as it stands when
	// that pass arms. Advancing three times up front would therefore fire
	// only the first: the clock has to move again after each wake, which is
	// what advancing inside the poll does.
	require.Eventually(t, func() bool {
		clk.Advance(3 * time.Hour)

		return hit.Load()
	}, 15*time.Second, 100*time.Millisecond,
		"every iteration completes and the host follows the outgoing flow")

	require.Eventually(t, func() bool {
		r, found, _ := repo.Load(context.Background(), instID)

		return found && r.Status == repository.StatusCompleted
	}, 5*time.Second, 10*time.Millisecond,
		"the woken instance runs to completion")
}
