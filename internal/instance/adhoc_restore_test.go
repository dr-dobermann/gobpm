package instance

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
)

// SRD-083 — the Ad-Hoc container's checkpoint fidelity: capture
// records the routing state (M1), restore rebuilds it (M2).

// adHocSnapshot builds start → triage(ad-hoc: a, b) → end. "a" is an
// instant service task; "b" is a sub-process parking on a gated
// conditional catch, so a routed "b" holds the container in flight at
// a durable wait. Inner ids are pinned for deterministic Router
// scripts and assertions.
func adHocSnapshot(
	t *testing.T, key string, r *scriptedRouter, gate *atomic.Int32,
	op service.Operation, adHocOpts ...options.Option,
) *snapshot.Snapshot {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New(key, foundation.WithID(key))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start",
		foundation.WithID(key+"-start"))
	require.NoError(t, err)

	triage, err := activities.NewSubProcess("triage",
		append([]options.Option{
			activities.WithAdHoc(r),
			foundation.WithID(key + "-triage"),
		}, adHocOpts...)...)
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

	catch, err := events.NewIntermediateCatchEvent("b-catch",
		gatedCond(t, gate), foundation.WithID(key+"-b-catch"))
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

	s, err := snapshot.New(p)
	require.NoError(t, err)

	return s
}

// adHocRec returns the document's single container record, or nil.
func adHocRec(d *checkpoint.Document) *checkpoint.AdHocRecord {
	if len(d.AdHoc) == 0 {
		return nil
	}

	return &d.AdHoc[0]
}

// TestAdHocCaptureRecordsRoutingState is the M1 capture pin (SRD-083
// FR-2): with "a" settled and "b" parked in flight, the checkpoint
// carries the container's record — the completed count for "a", no
// offer, not stopped — and the routed track's AdHocActivity.
func TestAdHocCaptureRecordsRoutingState(t *testing.T) {
	const key = "cr-ah"

	var gate atomic.Int32

	r := &scriptedRouter{turns: [][]string{{key + "-a"}, {key + "-b"}}}

	s := adHocSnapshot(t, key, r, &gate, instantOp(t, key+"-op"))

	doc := captureAt(t, s, func(d *checkpoint.Document) bool {
		rec := adHocRec(d)

		return rec != nil && rec.Completed[key+"-a"] == 1 &&
			trackAtAdHoc(d, key+"-b") != nil
	})

	rec := adHocRec(doc)
	require.Equal(t, map[string]int{key + "-a": 1}, rec.Completed)
	require.Empty(t, rec.Offered)
	require.False(t, rec.Stopped)
	require.Empty(t, rec.StopReason)

	routed := trackAtAdHoc(doc, key+"-b")
	require.Equal(t, key+"-b", routed.AdHocActivity,
		"the routed track records its inner-activity assignment")
}

// TestAdHocCaptureRecordsPendingOffer: a manual container's held offer
// rides the checkpoint (SRD-083 FR-2) — the scope-open persist point
// runs after the seed's routing, so the offer is never in a gap.
func TestAdHocCaptureRecordsPendingOffer(t *testing.T) {
	const key = "cr-aho"

	var gate atomic.Int32

	r := &scriptedRouter{turns: [][]string{{key + "-a", key + "-b"}}}

	s := adHocSnapshot(t, key, r, &gate, instantOp(t, key+"-op"),
		activities.WithAdHocManualSelection())

	doc := captureAt(t, s, func(d *checkpoint.Document) bool {
		rec := adHocRec(d)

		return rec != nil && len(rec.Offered) == 2
	})

	rec := adHocRec(doc)
	require.ElementsMatch(t,
		[]string{key + "-a", key + "-b"}, rec.Offered)
	require.Empty(t, rec.Completed)
	require.False(t, rec.Stopped)
}

// trackAtAdHoc finds the routed track for the given inner activity.
func trackAtAdHoc(
	d *checkpoint.Document, activityID string,
) *checkpoint.TrackRecord {
	for i := range d.Tracks {
		if d.Tracks[i].AdHocActivity == activityID {
			return &d.Tracks[i]
		}
	}

	return nil
}

// instantOp is a service operation that returns at once, counting its
// runs when a counter is given.
func instantOp(t *testing.T, name string, count ...*atomic.Int32) service.Operation {
	t.Helper()

	op, err := gooper.New(name,
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			for _, c := range count {
				c.Add(1)
			}

			return nil, nil
		})
	require.NoError(t, err)

	return op
}

// docCopy deep-copies a document over the wire form, so refusal
// subtests can each mutate their own.
func docCopy(t *testing.T, doc *checkpoint.Document) *checkpoint.Document {
	t.Helper()

	raw, err := doc.Marshal()
	require.NoError(t, err)

	back, err := checkpoint.Unmarshal(raw)
	require.NoError(t, err)

	return back
}

// TestAdHocRestoresRoutingMidFlight is T-2 (SRD-083 FR-3/FR-4): the
// automatic container restores at its position — "a" does not re-run,
// the post-restore Router ask sees the true progress, and the
// container completes with the unkilled run's outcome.
func TestAdHocRestoresRoutingMidFlight(t *testing.T) {
	const key = "cr-ahr"

	var gate, count atomic.Int32

	r := &scriptedRouter{turns: [][]string{{key + "-a"}, {key + "-b"}}}

	s := adHocSnapshot(t, key, r, &gate, instantOp(t, key+"-op", &count))

	doc := captureAt(t, s, func(d *checkpoint.Document) bool {
		rec := adHocRec(d)

		return rec != nil && rec.Completed[key+"-a"] == 1 &&
			trackAtAdHoc(d, key+"-b") != nil
	})

	require.Equal(t, int32(1), count.Load())

	asked := len(r.states())

	gate.Store(1) // open b's catch: the restored container can drain
	restoreToDone(t, doc, s)

	require.Equal(t, int32(1), count.Load(),
		"the completed activity must not re-run across the crash")

	states := r.states()
	require.Greater(t, len(states), asked,
		"the settle of the restored activity consults the Router")

	last := states[len(states)-1]
	require.Equal(t, map[string]int{key + "-a": 1, key + "-b": 1},
		last.Completed, "the Router sees the true cross-crash progress")
	require.Empty(t, last.Running)
	require.Equal(t, key+"-b", last.Last)
}

// TestAdHocManualOfferRestores is T-3 (SRD-083 FR-3/FR-4): the
// restored offer is visible, consumable, and the container completes.
func TestAdHocManualOfferRestores(t *testing.T) {
	const key = "cr-ahm"

	var gate, count atomic.Int32

	r := &scriptedRouter{turns: [][]string{{key + "-a"}}}

	s := adHocSnapshot(t, key, r, &gate, instantOp(t, key+"-op", &count),
		activities.WithAdHocManualSelection())

	doc := captureAt(t, s, func(d *checkpoint.Document) bool {
		rec := adHocRec(d)

		return rec != nil && len(rec.Offered) == 1
	})

	restored, err := Restore(doc, s, scope.EmptyDataPath,
		cpRuntime(t), laxEP(t), nil, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, restored.Run(ctx))

	offered, running, err := restored.AdHocView(ctx, key+"-triage")
	require.NoError(t, err)
	require.Equal(t, []string{key + "-a"}, offered,
		"the held offer survives the crash")
	require.Empty(t, running)

	require.NoError(t,
		restored.ActivateAdHoc(ctx, key+"-triage", key+"-a"))

	select {
	case <-restored.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("the restored manual container did not finish")
	}

	require.Equal(t, Completed, restored.State())
	require.Equal(t, int32(1), count.Load(),
		"the activated offer ran exactly once")
}

// TestAdHocRestoreHonorsRecordedStop is T-4 (SRD-083 FR-4): a stopped
// container runs no further routing after restore — the in-flight
// activity drains it.
func TestAdHocRestoreHonorsRecordedStop(t *testing.T) {
	const key = "cr-ahs"

	var gate atomic.Int32

	r := &scriptedRouter{turns: [][]string{{key + "-a"}, {key + "-b"}}}

	s := adHocSnapshot(t, key, r, &gate, instantOp(t, key+"-op"))

	doc := captureAt(t, s, func(d *checkpoint.Document) bool {
		rec := adHocRec(d)

		return rec != nil && trackAtAdHoc(d, key+"-b") != nil
	})

	// what the container would have recorded had its completion
	// condition fired while "b" was still draining.
	rec := adHocRec(doc)
	rec.Stopped = true
	rec.StopReason = adHocStopCompletionCond

	asked := len(r.states())

	gate.Store(1)
	restoreToDone(t, doc, s)

	require.Equal(t, asked, len(r.states()),
		"a stopped container consults the Router no further")
}

// TestAdHocRestoreCompletesStoppedEmpty pins the cancel-window edge
// (SRD-083 FR-4): a stopped container captured with nothing live — no
// routed track survived to drive the drain — completes at adoption
// instead of hanging.
func TestAdHocRestoreCompletesStoppedEmpty(t *testing.T) {
	const key = "cr-ahe"

	var gate atomic.Int32

	r := &scriptedRouter{turns: [][]string{{key + "-a"}}}

	s := adHocSnapshot(t, key, r, &gate, instantOp(t, key+"-op"),
		activities.WithAdHocManualSelection())

	doc := captureAt(t, s, func(d *checkpoint.Document) bool {
		rec := adHocRec(d)

		return rec != nil && len(rec.Offered) == 1
	})

	// the cancel-window shape: stopped, no offer, no live routed work.
	rec := adHocRec(doc)
	rec.Stopped = true
	rec.StopReason = adHocStopCompletionCond
	rec.Offered = nil

	restoreToDone(t, doc, s)
}

// TestAdHocSequentialInvariantAfterRestore (SRD-083 FR-4): the
// sequential-ordering guard counts RESTORED live work — a
// multi-successor answer after the restore is rejected exactly as it
// is on a fresh run.
func TestAdHocSequentialInvariantAfterRestore(t *testing.T) {
	const key = "cr-ahq"

	var gate atomic.Int32

	r := &scriptedRouter{
		turns: [][]string{{key + "-a"}, {key + "-b"},
			{key + "-a", key + "-b"}},
	}

	s := adHocSnapshot(t, key, r, &gate, instantOp(t, key+"-op"),
		activities.WithAdHocOrdering(activities.AdHocSequential))

	doc := captureAt(t, s, func(d *checkpoint.Document) bool {
		rec := adHocRec(d)

		return rec != nil && trackAtAdHoc(d, key+"-b") != nil
	})

	// post-restore, b's settle draws the scripted TWO-successor answer.
	gate.Store(1)
	restoreExpectFault(t, doc, s, "only one may run at a time")
}

// TestAdHocRestoreRefusals is T-5 (SRD-083 FR-5/FR-6): every
// inconsistent record — and the pre-fidelity document — refuses with
// its cause.
func TestAdHocRestoreRefusals(t *testing.T) {
	const key = "cr-ahf"

	var gate atomic.Int32

	r := &scriptedRouter{turns: [][]string{{key + "-a"}, {key + "-b"}}}

	s := adHocSnapshot(t, key, r, &gate, instantOp(t, key+"-op"))

	base := captureAt(t, s, func(d *checkpoint.Document) bool {
		rec := adHocRec(d)

		return rec != nil && rec.Completed[key+"-a"] == 1 &&
			trackAtAdHoc(d, key+"-b") != nil
	})

	t.Run("a ghost host track", func(t *testing.T) {
		doc := docCopy(t, base)
		adHocRec(doc).HostTrack = "ghost"

		restoreExpectFault(t, doc, s, "the track table does not carry")
	})

	t.Run("a scope the table does not hold", func(t *testing.T) {
		doc := docCopy(t, base)
		adHocRec(doc).ScopePath = "/nowhere"

		restoreExpectFault(t, doc, s,
			"not in the document's scope table")
	})

	t.Run("an unresolvable offered id", func(t *testing.T) {
		doc := docCopy(t, base)
		adHocRec(doc).Offered = []string{"nope"}

		restoreExpectFault(t, doc, s, "doesn't resolve")
	})

	t.Run("a record naming a plain composite", func(t *testing.T) {
		doc := docCopy(t, base)

		// b's own child scope is open in the capture — a three-segment
		// path hosted by the PLAIN sub-process "b".
		var bScope string

		for _, sc := range doc.Scopes {
			if strings.Count(sc.Path, "/") == 3 {
				bScope = sc.Path
			}
		}

		require.NotEmpty(t, bScope)

		doc.AdHoc = append(doc.AdHoc, checkpoint.AdHocRecord{
			HostTrack: trackAtAdHoc(doc, key+"-b").ID,
			ScopePath: bScope,
		})

		restoreExpectFault(t, doc, s, "is not an Ad-Hoc container")
	})

	t.Run("a pre-fidelity document", func(t *testing.T) {
		doc := docCopy(t, base)
		doc.AdHoc = nil

		restoreExpectFault(t, doc, s,
			"predates ad-hoc checkpoint fidelity")
	})
}

// TestSchemaFourNoAdHocRestores is T-6 (SRD-083 FR-1/FR-6): a
// schema-4 document without ad-hoc work restores exactly as today.
func TestSchemaFourNoAdHocRestores(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	_, inst, doc, cancel := parkAndInspect(t)
	cancel()

	raw, err := doc.Marshal()
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	m["schema"] = 4
	delete(m, "adhoc")

	old, err := json.Marshal(m)
	require.NoError(t, err)

	back, err := checkpoint.Unmarshal(old)
	require.NoError(t, err)
	require.Equal(t, 4, back.Schema)

	restored, err := Restore(back, condSnapshotFor(t, back),
		scope.EmptyDataPath, cpRuntime(t), laxEP(t), nil, nil)
	require.NoError(t, err)
	require.Equal(t, inst.ID(), restored.ID())
}
