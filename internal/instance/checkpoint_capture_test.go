package instance

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/repository"
)

// cpSink collects reported facts for the degradation assertions.
type cpSink struct {
	facts []observability.Fact
}

func (cs *cpSink) Report(f observability.Fact) {
	cs.facts = append(cs.facts, f)
}

func (cs *cpSink) has(phase observability.Phase) bool {
	for _, f := range cs.facts {
		if f.Kind == observability.KindInstanceState && f.Phase == phase {
			return true
		}
	}

	return false
}

// parkAndInspect runs a conditional-catch process to its park under an
// armed checkpoint and returns the runtime, the instance and the parked
// record's document.
func parkAndInspect(t *testing.T) (
	*enginert.Runtime, *Instance, *checkpoint.Document, context.CancelFunc,
) {
	t.Helper()

	val := false
	evals := 0

	def, err := events.NewConditionalEventDefinition(
		condExpr(t, &val, &evals))
	require.NoError(t, err)

	s := condSnapshot(t, def)
	s.Version = 7 // the FR-1 pin a registration would stamp

	ep := mockeventproc.NewMockEventProducer(t)
	rt := enginert.Default()

	inst, err := New(s, scope.EmptyDataPath, rt, ep, nil,
		WithCheckpointing("engine-A", time.Minute))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, inst.Run(ctx))

	// the park transition writes the checkpoint — poll the repository
	// for the record carrying the parked track.
	var doc *checkpoint.Document

	require.Eventually(t, func() bool {
		rec, ok, err := rt.Repository().Load(ctx, inst.ID())
		if err != nil || !ok {
			return false
		}

		d, err := checkpoint.Unmarshal(rec.Payload)
		if err != nil {
			return false
		}

		for _, tr := range d.Tracks {
			if tr.State == "TrackWaitForEvent" {
				doc = d

				return true
			}
		}

		return false
	}, 2*time.Second, 5*time.Millisecond,
		"the park transition must checkpoint the parked track")

	return rt, inst, doc, cancel
}

// condSnapshot builds the start → conditional catch → end snapshot.
func condSnapshot(
	t *testing.T, def *events.ConditionalEventDefinition,
) *snapshot.Snapshot {
	t.Helper()

	p, err := process.New("cp-cond")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	catch, err := events.NewIntermediateCatchEvent("cond-catch", def)
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, catch, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, catch)
	link(t, catch, end)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	return s
}

// TestCheckpointLifecycle covers SRD-070 T-2's happy half: the park
// checkpoint's content and the terminal record (through the Terminate
// cancellation — the WithoutCancel write).
func TestCheckpointLifecycle(t *testing.T) {
	rt, inst, doc, cancel := parkAndInspect(t)
	defer cancel()

	require.Equal(t, inst.ID(), doc.InstanceID)
	require.Equal(t, inst.s.ProcessID, doc.ProcessID)
	require.Equal(t, 7, doc.Version, "the FR-1 pin must ride the document")
	require.Equal(t, "Active", doc.Status)
	require.NotEmpty(t, doc.Scopes, "the root scope must be recorded")
	require.Len(t, doc.Tracks, 1, "one live parked track")
	require.NotEmpty(t, doc.Tracks[0].NodeID)

	// terminate → the terminal record must land despite the canceled ctx.
	cancel()

	require.Eventually(t, func() bool {
		rec, ok, _ := rt.Repository().Load(context.Background(), inst.ID())

		return ok && rec.Status == repository.StatusTerminated
	}, 2*time.Second, 5*time.Millisecond,
		"the terminal checkpoint must flip the persisted status")

	rec, _, _ := rt.Repository().Load(context.Background(), inst.ID())
	require.GreaterOrEqual(t, rec.RecVersion, int64(2),
		"activation + park + terminal writes advance the CAS version")
	require.Equal(t, "engine-A", rec.Lease.Owner)
}

// TestCheckpointDeferGuard covers the defer-don't-die list: an in-flight
// construct defers the write with the Warn fact instead of a torn
// document or a dead instance.
func TestCheckpointDeferGuard(t *testing.T) {
	val := false
	evals := 0

	def, err := events.NewConditionalEventDefinition(
		condExpr(t, &val, &evals))
	require.NoError(t, err)

	s := condSnapshot(t, def)

	sink := &cpSink{}
	rt := enginert.Default().WithReporter(sink)
	ep := mockeventproc.NewMockEventProducer(t)

	inst, err := New(s, scope.EmptyDataPath, rt, ep, nil,
		WithCheckpointing("engine-A", time.Minute))
	require.NoError(t, err)

	ls := newLoopState(inst)
	ls.calls["busy"] = nil // a Call Activity in flight

	ls.checkpointNow(context.Background())

	require.True(t, sink.has(observability.PhaseCheckpointDeferred),
		"the degradation must be operator-visible")

	_, ok, _ := rt.Repository().Load(context.Background(), inst.ID())
	require.False(t, ok, "no torn document may be written")
}

// TestCheckpointDisabledByDefault: without the option the instance
// stays volatile — no record, no facts, today's behavior.
func TestCheckpointDisabledByDefault(t *testing.T) {
	val := false
	evals := 0

	def, err := events.NewConditionalEventDefinition(
		condExpr(t, &val, &evals))
	require.NoError(t, err)

	s := condSnapshot(t, def)

	rt := enginert.Default()
	ep := mockeventproc.NewMockEventProducer(t)

	inst, err := New(s, scope.EmptyDataPath, rt, ep, nil)
	require.NoError(t, err)

	ls := newLoopState(inst)
	ls.checkpointNow(context.Background())

	_, ok, _ := rt.Repository().Load(context.Background(), inst.ID())
	require.False(t, ok)
}

// TestCaptureArms lifts the remaining capture branches: the other defer
// guards, the ledger records (folded recursion), the encode-error
// defer, a fenced save, the conversation keys and the status mapping.
func TestCaptureArms(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	newArmedInstance := func(t *testing.T) (*Instance, *loopState, *cpSink) {
		t.Helper()

		val := false
		evals := 0

		def, err := events.NewConditionalEventDefinition(
			condExpr(t, &val, &evals))
		require.NoError(t, err)

		sink := &cpSink{}
		rt := enginert.Default().WithReporter(sink)
		ep := mockeventproc.NewMockEventProducer(t)

		inst, err := New(condSnapshot(t, def), scope.EmptyDataPath, rt, ep,
			nil, WithCheckpointing("engine-A", time.Minute))
		require.NoError(t, err)

		return inst, newLoopState(inst), sink
	}

	t.Run("MI and sweep guards defer",
		func(t *testing.T) {
			inst, ls, _ := newArmedInstance(t)

			ls.miGroups["g"] = nil
			_, reason := ls.captureDocument(context.Background())
			require.Contains(t, reason, "multi-instance")

			delete(ls.miGroups, "g")
			ls.sweeps["s"] = nil
			_, reason = ls.captureDocument(context.Background())
			require.Contains(t, reason, "compensation sweep")

			_ = inst
		})

	t.Run("ledger entries flatten with their folded children",
		func(t *testing.T) {
			inst, ls, _ := newArmedInstance(t)

			ls.ledgers[inst.sc.root] = []*ledgerEntry{{
				activityID: "book",
				handlerID:  "undo-book",
				ordinal:    0,
				folded: []*ledgerEntry{{
					activityID: "inner",
					ordinal:    0,
				}},
			}}

			doc, reason := ls.captureDocument(context.Background())
			require.Empty(t, reason)
			require.Len(t, doc.Ledgers, 2, "the folded child records too")
			require.Equal(t, "book", doc.Ledgers[0].ActivityID)
			require.Equal(t, "inner", doc.Ledgers[1].ActivityID)
		})

	t.Run("an uncodable committed payload defers with encode context",
		func(t *testing.T) {
			inst, ls, _ := newArmedInstance(t)

			bad, err := data.ReadyValueParameter("hot",
				values.NewVariable(make(chan int)))
			require.NoError(t, err)

			_, err = inst.sc.plane.Commit(inst.sc.root, bad)
			require.NoError(t, err)

			_, reason := ls.captureDocument(context.Background())
			require.Contains(t, reason, "encode:")
		})

	t.Run("a fenced save degrades to the fact",
		func(t *testing.T) {
			inst, ls, sink := newArmedInstance(t)

			// a foreign write bumps the stored version — CAS fences ours.
			require.NoError(t, inst.Repository().Save(context.Background(),
				repository.InstanceRecord{ID: inst.ID(),
					Status: repository.StatusActive}))

			ls.checkpointNow(context.Background())
			require.True(t,
				sink.has(observability.PhaseCheckpointDeferred))
		})

	t.Run("conversation keys ride the document",
		func(t *testing.T) {
			inst, ls, _ := newArmedInstance(t)

			inst.corr.keys["orderID"] = "42"

			doc, reason := ls.captureDocument(context.Background())
			require.Empty(t, reason)
			require.Equal(t, "42", doc.ConvKeys["orderID"])
		})

	t.Run("an unknown runtime state persists as Active",
		func(t *testing.T) {
			require.Equal(t, repository.StatusActive, persistedStatus(State(99)))
			require.Equal(t, repository.StatusCompleted,
				persistedStatus(Completed))
		})
}
