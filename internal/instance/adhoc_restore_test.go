package instance

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
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

// instantOp is a service operation that returns at once.
func instantOp(t *testing.T, name string) service.Operation {
	t.Helper()

	op, err := gooper.New(name,
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			return nil, nil
		})
	require.NoError(t, err)

	return op
}
