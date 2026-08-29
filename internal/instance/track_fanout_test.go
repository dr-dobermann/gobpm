package instance

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
)

// TestAConcurrentInstanceParksUnderItsOwnOrdinal (ADR-025 §2.13b.1e): the
// track's current executor is the DECORATOR, whose ordinal is the activity's —
// so an instance that asks for an identity must be answered about itself.
//
// Answered about the activity, N instances announced one task between them.
func TestAConcurrentInstanceParksUnderItsOwnOrdinal(t *testing.T) {
	_, tr, d := fanOutTrack(t)

	e := &nodeExec{t: tr, step: tr.steps[0], ord: 2, concurrent: true}

	id, ord := tr.humanTaskIdentity(e)

	require.Equal(t, 2, ord, "its own ordinal, not the decorator's")
	require.NotEmpty(t, id)
	require.Equal(t, id, d.taskIDFor(2), "and the identity is the decorator's to keep")
	require.True(t, d.anyWaiting(), "asking for an identity parks it")

	other, otherOrd := tr.humanTaskIdentity(
		&nodeExec{t: tr, step: tr.steps[0], ord: 0, concurrent: true})
	require.Equal(t, 0, otherOrd)
	require.NotEqual(t, id, other, "a sibling is a different task")
}

// TestParkingAHumanTaskMarksTHATExecutionAsWaiting (ADR-025 §2.13, "a node
// executor owns that node's wait"): the flag is the executor's, because a
// track shared by N instances cannot say which of them is parked.
func TestParkingAHumanTaskMarksTHATExecutionAsWaiting(t *testing.T) {
	_, tr, _ := fanOutTrack(t)

	e := &nodeExec{t: tr, step: tr.steps[0], ord: 1, concurrent: true}
	require.False(t, e.parked.Load())

	require.NoError(t, tr.parkHumanTask(e, tr.steps[0].node))
	require.True(t, e.parked.Load(),
		"reading the track's state instead would let one instance's park "+
			"stand for all of them, and the barrier would proceed on the first")
}

// TestTheIdentityRegisterIsLiveNotASnapshot (ADR-020 §2.12): the capture
// cannot read the identities off the executor, because a release clears it
// before the cut that records the wait — so the TRACK keeps them, recorded as
// each instance mints one.
//
// Recorded as minted rather than snapshotted at the release, because a release
// can land part-way through parking N instances. Such a snapshot holds only
// the identities minted so far, and every later capture preferred that stale
// set to the live one — so a restored fan-out minted a fresh id for the
// instance the snapshot had missed, and the handle its holder was carrying
// named nothing.
func TestTheIdentityRegisterIsLiveNotASnapshot(t *testing.T) {
	_, tr, _ := fanOutTrack(t)

	require.Nil(t, tr.taskIDRegister(), "nothing parked, nothing recorded")

	zero, _ := tr.humanTaskIdentity(
		&nodeExec{t: tr, step: tr.steps[0], ord: 0, concurrent: true})
	require.Equal(t, map[int]string{0: zero}, tr.taskIDRegister(),
		"recorded as it is minted, not at some later moment")

	one, _ := tr.humanTaskIdentity(
		&nodeExec{t: tr, step: tr.steps[0], ord: 1, concurrent: true})
	require.Equal(t, map[int]string{0: zero, 1: one}, tr.taskIDRegister(),
		"and the second joins it — a capture between the two must not fix "+
			"the set at what it saw")

	// the register is a COPY: the capture must not reach into the live set.
	tr.taskIDRegister()[0] = "tampered"
	require.Equal(t, zero, tr.taskIDRegister()[0])

	tr.forgetTaskID(0)
	require.Equal(t, map[int]string{1: one}, tr.taskIDRegister(),
		"an accounted-for instance keeps no identity — a later pass of the "+
			"same activity mints its own")
}

// TestAFanOutReadsBusyWhileItHasWorkToApply: the loop asks before releasing a
// track, and the track's own state cannot answer — it reads WaitForEvent
// because its instances are parked, whether or not one of their completions is
// waiting to be applied.
//
// The DECORATOR answers, because it is what executes the instances (ADR-025
// §2.15a).
func TestAFanOutReadsBusyWhileItHasWorkToApply(t *testing.T) {
	_, tr, d := fanOutTrack(t)

	require.False(t, tr.iterationsBusy(), "everyone parked, nothing to apply")

	d.parking(0)
	d.deliver(0, sigDefN(t, "done"))
	require.True(t, tr.iterationsBusy(),
		"a queued completion is work outstanding, and releasing here would "+
			"take the track away before it was applied")

	d.takeDeliveries()
	d.delivering()
	require.True(t, tr.iterationsBusy(), "and now it is being applied")

	d.delivered()
	require.False(t, tr.iterationsBusy(),
		"applied and accounted for — the loop is free to release")
}

// TestARestoredFanOutOffersOnlyTheWorkStillOutstanding (ADR-020 §2.12): the
// identities come back per ordinal, and a COMPLETED instance is skipped — its
// task was withdrawn when it was done, and re-registering it would offer work
// nobody can do.
func TestARestoredFanOutOffersOnlyTheWorkStillOutstanding(t *testing.T) {
	_, tr, _ := fanOutTrack(t)

	require.Empty(t, tr.seededTaskIDs(), "no seed, nothing to re-register")

	tr.iterSeed = &checkpoint.IterationRecord{
		N: 3,
		Instances: []checkpoint.IterationEntry{
			{Ordinal: 0, State: iterationCompleted, TaskID: "done-0"},
			{Ordinal: 1, State: iterationRunning, TaskID: "live-1"},
			{Ordinal: 2, State: iterationRunning},
		},
	}

	require.Equal(t, map[int]string{1: "live-1"}, tr.seededTaskIDs(),
		"the completed ordinal is not re-offered, and one that recorded no "+
			"identity has none to give back")
}

// TestNothingIsRecordedForAnIterationWithoutAnIdentity: only a HUMAN task has a
// parked-work identity, so an iterated activity over anything else records
// none — and an empty id must not become a register entry naming nothing.
func TestNothingIsRecordedForAnIterationWithoutAnIdentity(t *testing.T) {
	_, tr, _ := fanOutTrack(t)

	tr.rememberTaskID(0, "")
	require.Nil(t, tr.taskIDRegister())
}

// TestAnOccurrenceIsDroppedRatherThanStallingTheLoop: the LOOP is the sender,
// so a pass that is not reading must never block the single writer (SRD-027
// FR-4's rule).
func TestAnOccurrenceIsDroppedRatherThanStallingTheLoop(t *testing.T) {
	_, tr, _ := fanOutTrack(t)
	def := sigDefN(t, "sig")

	for range eventBufferDepth {
		require.True(t, tr.offerToPass(def), "the channel takes what it holds")
	}

	require.False(t, tr.offerToPass(def),
		"and the next is dropped, rather than stalling the loop on a pass "+
			"that is not reading")
}
