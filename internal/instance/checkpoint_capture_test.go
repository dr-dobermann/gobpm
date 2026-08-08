package instance

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/stretchr/testify/mock"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/repository"
)

// lastCondSnapshot shares parkAndInspect's template with the restore
// tests (stable node ids — the deployment-parity requirement).
var lastCondSnapshot *snapshot.Snapshot

// cpSink collects reported facts for the degradation assertions.
// Mutex-guarded: tracks report from their own goroutines while a test
// polls has() (SRD-082's capture tests do).
type cpSink struct {
	mu    sync.Mutex
	facts []observability.Fact
}

func (cs *cpSink) Report(f observability.Fact) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	cs.facts = append(cs.facts, f)
}

// hasDeferralReason reports a CheckpointDeferred fact whose reason
// carries the given substring (independent-review note T3 — a
// deferral for an unrelated cause must not satisfy a deferral test).
func (cs *cpSink) hasDeferralReason(sub string) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	for _, f := range cs.facts {
		if f.Phase == observability.PhaseCheckpointDeferred &&
			strings.Contains(f.Details["reason"], sub) {
			return true
		}
	}

	return false
}

func (cs *cpSink) has(phase observability.Phase) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	for _, f := range cs.facts {
		if f.Kind == observability.KindInstanceState && f.Phase == phase {
			return true
		}
	}

	return false
}

// engineA is the checkpoint owner and engine group the capture tests
// arm.
const engineA = "engine-A"

// cpRuntime builds the default runtime with engineA's group already
// established — mirroring the RegisterGroup the engine issues at Run
// (SRD-078 FR-2) before any checkpoint save.
func cpRuntime(t *testing.T) *enginert.Runtime {
	t.Helper()

	rt := enginert.Default()
	require.NoError(t,
		rt.Repository().RegisterGroup(context.Background(), engineA))

	return rt
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
	lastCondSnapshot = s

	ep := mockeventproc.NewMockEventProducer(t)
	rt := cpRuntime(t)

	inst, err := New(s, scope.EmptyDataPath, rt, ep, nil,
		WithCheckpointing("engine-A", "engine-A", time.Minute))
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
	rt := cpRuntime(t).WithReporter(sink)
	ep := mockeventproc.NewMockEventProducer(t)

	inst, err := New(s, scope.EmptyDataPath, rt, ep, nil,
		WithCheckpointing("engine-A", "engine-A", time.Minute))
	require.NoError(t, err)

	// no construct guards remain (SRD-082 FR-8, M4): an in-flight call
	// is RECORDED — deferral means only a real encode/save failure.
	ls := newLoopState(inst)
	tr := &track{
		BaseElement: *foundation.MustBaseElement(),
		steps:       []*stepInfo{{node: findNode(t, s, "cond-catch")}},
	}
	ls.calls["busy"] = &callEntry{track: tr, node: tr.currentStep().node}

	ls.checkpointNow(context.Background())

	require.False(t, sink.has(observability.PhaseCheckpointDeferred),
		"an in-flight call captures — the deferral posture is retired")

	rec, ok, _ := rt.Repository().Load(context.Background(), inst.ID())
	require.True(t, ok, "the document is written")

	d, err := checkpoint.Unmarshal(rec.Payload)
	require.NoError(t, err)
	require.Len(t, d.Calls, 1)
	require.Equal(t, "busy", d.Calls[0].ChildID)
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
		rt := cpRuntime(t).WithReporter(sink)
		ep := mockeventproc.NewMockEventProducer(t)

		inst, err := New(condSnapshot(t, def), scope.EmptyDataPath, rt, ep,
			nil, WithCheckpointing("engine-A", "engine-A", time.Minute))
		require.NoError(t, err)

		return inst, newLoopState(inst), sink
	}

	t.Run("no construct defers; every position is recorded",
		func(t *testing.T) {
			inst, ls, _ := newArmedInstance(t)
			host := inst.tracks[firstTrackID(inst)]

			// every guard retired (SRD-082 FR-8): groups in M2, sweeps
			// in M3, calls in M4 — all recorded now, never deferred.
			ls.miGroups["g"] = &miGroup{host: host}
			ls.sweeps["s"] = &sweepRun{
				sweep: &compSweep{path: inst.sc.root, thrower: host,
					wait: true},
				entry: &ledgerEntry{activityID: "a-1", ordinal: 0},
			}
			ls.calls["c"] = &callEntry{
				track: host, node: host.currentStep().node,
			}
			doc, reason := ls.captureDocument(context.Background())
			require.Empty(t, reason)
			require.Len(t, doc.MIGroups, 1)
			require.Len(t, doc.Sweeps, 1)
			require.Equal(t, host.ID(), doc.Sweeps[0].ThrowerTrack)
			require.NotNil(t, doc.Sweeps[0].Running)
			require.Len(t, doc.Calls, 1)
			require.Equal(t, "c", doc.Calls[0].ChildID)
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
					Status: repository.StatusActive,
					Group:  engineA}))

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

// TestRestore covers SRD-070 T-4: the captured document rebuilds an
// equal instance — scopes, data, keys, the parked track — and the
// guards are loud.
func TestRestore(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	rt, inst, doc, cancel := parkAndInspect(t)
	cancel() // the source instance is done serving

	t.Run("the document rebuilds the instance",
		func(t *testing.T) {
			// the pinned version must match the snapshot exactly.
			s2 := condSnapshotFor(t, doc)

			ep := mockeventproc.NewMockEventProducer(t)
			ep.EXPECT().
				RegisterEvent(mock.Anything, mock.Anything).
				Return(nil).
				Maybe()

			restored, err := Restore(doc, s2, scope.EmptyDataPath,
				enginert.Default(), ep, nil, nil,
				WithCheckpointCursor(5, 2))
			require.NoError(t, err)

			require.Equal(t, inst.ID(), restored.ID(),
				"the recorded identity survives")
			require.Equal(t, int64(5), restored.cpRecVersion)
			require.Equal(t, int64(2), restored.cpIncarnation)
			require.Len(t, restored.tracks, 1,
				"the live parked track rebuilt")

			for _, tr := range restored.tracks {
				require.Equal(t, doc.Tracks[0].NodeID,
					tr.currentStep().node.ID(),
					"the track re-enters its recorded node")
			}

			_ = rt
		})

	t.Run("a version mismatch is loud",
		func(t *testing.T) {
			s2 := condSnapshotFor(t, doc)
			s2.Version = doc.Version + 1

			ep := mockeventproc.NewMockEventProducer(t)

			_, err := Restore(doc, s2, scope.EmptyDataPath,
				enginert.Default(), ep, nil, nil)
			require.Error(t, err)
			require.Contains(t, err.Error(), "pinned version")
		})

	t.Run("a nil document is loud",
		func(t *testing.T) {
			_, err := Restore(nil, nil, scope.EmptyDataPath,
				enginert.Default(), nil, nil, nil)
			require.Error(t, err)
		})

	t.Run("a recorded node missing from the version is loud",
		func(t *testing.T) {
			s2 := condSnapshotFor(t, doc)

			broken := *doc
			broken.Tracks = append([]checkpoint.TrackRecord{},
				doc.Tracks...)
			broken.Tracks[0].NodeID = "no-such-node"

			ep := mockeventproc.NewMockEventProducer(t)

			_, err := Restore(&broken, s2, scope.EmptyDataPath,
				enginert.Default(), ep, nil, nil)
			require.Error(t, err)
			require.Contains(t, err.Error(), "isn't in the pinned")
		})

	t.Run("restored ledgers rebuild with their snapshots",
		func(t *testing.T) {
			s2 := condSnapshotFor(t, doc)

			withLedger := *doc

			enc, err := checkpoint.EncodeData(context.Background(), "/x",
				[]data.Data{mustParam(t, "saved", 7)})
			require.NoError(t, err)

			withLedger.Ledgers = []checkpoint.LedgerRecord{{
				ScopePath:  string(scope.RootDataPath),
				ActivityID: "book",
				Ordinal:    0,
				Snapshot:   enc,
			}}
			withLedger.Tracks = nil

			ep := mockeventproc.NewMockEventProducer(t)

			restored, err := Restore(&withLedger, s2, scope.EmptyDataPath,
				enginert.Default(), ep, nil, nil)
			require.NoError(t, err)
			require.Len(t,
				restored.restoredLedgers[scope.RootDataPath], 1)
			require.Equal(t, "saved",
				restored.restoredLedgers[scope.RootDataPath][0].
					snapshot[0].Name())

			// run it: the loop ADOPTS the restored ledger (and, with no
			// tracks, completes at once) — the adoption line is load-
			// bearing for post-restart compensability.
			runCtx, runCancel := context.WithCancel(context.Background())
			defer runCancel()

			require.NoError(t, restored.Run(runCtx))
			require.Eventually(t, func() bool {
				return restored.State() == Completed
			}, 2*time.Second, 5*time.Millisecond)
		})
}

// condSnapshotFor rebuilds the conditional snapshot carrying the
// document's pinned version (the same-definition contract; node ids
// differ per build, so the rebuild REUSES the source instance's
// snapshot template via Clone semantics — here the simplest equivalent:
// the recorded node must exist, so we clone from the parked instance's
// own template).
func condSnapshotFor(
	t *testing.T, doc *checkpoint.Document,
) *snapshot.Snapshot {
	t.Helper()

	s := lastCondSnapshot
	require.NotNil(t, s, "parkAndInspect must have built the snapshot")

	c, err := s.Clone()
	require.NoError(t, err)

	c.Version = doc.Version

	return c
}

// mustParam wraps an int as a Ready parameter.
func mustParam(t *testing.T, name string, v int) data.Data {
	t.Helper()

	p, err := data.ReadyValueParameter(name, values.NewVariable(v))
	require.NoError(t, err)

	return p
}

// TestRestoreArms lifts the remaining restore branches: child scopes,
// the reopen guard, and the timer descriptor → hint wiring.
func TestRestoreArms(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	_, _, doc, cancel := parkAndInspect(t)
	cancel()

	t.Run("a child scope reopens with its data",
		func(t *testing.T) {
			s2 := condSnapshotFor(t, doc)

			enc, err := checkpoint.EncodeData(context.Background(), "/c",
				[]data.Data{mustParam(t, "childD", 3)})
			require.NoError(t, err)

			withChild := *doc
			withChild.Tracks = nil
			withChild.Scopes = append(
				append([]checkpoint.ScopeRecord{}, doc.Scopes...),
				checkpoint.ScopeRecord{
					Path: doc.Scopes[0].Path + "/sp-x",
					Data: enc,
				})

			ep := mockeventproc.NewMockEventProducer(t)

			restored, err := Restore(&withChild, s2, scope.EmptyDataPath,
				enginert.Default(), ep, nil, nil)
			require.NoError(t, err)

			child := scope.DataPath(doc.Scopes[0].Path + "/sp-x")
			own, err := restored.sc.plane.OwnData(child)
			require.NoError(t, err)
			require.Len(t, own, 1)
			require.Equal(t, "childD", own[0].Name())
		})

	t.Run("an orphan scope path is loud",
		func(t *testing.T) {
			s2 := condSnapshotFor(t, doc)

			broken := *doc
			broken.Tracks = nil
			broken.Scopes = append(
				append([]checkpoint.ScopeRecord{}, doc.Scopes...),
				checkpoint.ScopeRecord{Path: "/elsewhere/deep"})

			ep := mockeventproc.NewMockEventProducer(t)

			_, err := Restore(&broken, s2, scope.EmptyDataPath,
				enginert.Default(), ep, nil, nil)
			require.Error(t, err)
			require.Contains(t, err.Error(), "reopen")
		})

	t.Run("a timer descriptor becomes the deadline hint",
		func(t *testing.T) {
			s2 := condSnapshotFor(t, doc)

			when := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

			withTimer := *doc
			withTimer.Tracks = append([]checkpoint.TrackRecord{},
				doc.Tracks...)
			withTimer.Tracks[0].Timer = &checkpoint.TimerDescriptor{
				Deadline:   when,
				CyclesLeft: 3,
			}

			ep := mockeventproc.NewMockEventProducer(t)
			ep.EXPECT().
				RegisterEvent(mock.Anything, mock.Anything).
				Return(nil).
				Maybe()

			restored, err := Restore(&withTimer, s2, scope.EmptyDataPath,
				enginert.Default(), ep, nil, nil)
			require.NoError(t, err)

			for _, tr := range restored.tracks {
				deadline, cycles, ok := tr.TimerDeadlineHint("any")
				require.True(t, ok, "a restored timer track must hint")
				require.True(t, when.Equal(deadline))
				require.Equal(t, 3, cycles)
			}
		})

	t.Run("a fresh track hints nothing",
		func(t *testing.T) {
			tr := &track{}
			_, _, ok := tr.TimerDeadlineHint("any")
			require.False(t, ok)
		})
}

// TestStashTimerPlan covers the park-time hook directly: a timer def
// stashes its plan, everything else stashes nothing.
func TestStashTimerPlan(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	when := time.Now().Add(time.Hour).Truncate(time.Second)

	timeExpr, err := goexpr.New(nil,
		data.MustItemDefinition(values.NewVariable(time.Time{})),
		func(_ context.Context, _ data.Source) (data.Value, error) {
			return values.NewVariable(when), nil
		})
	require.NoError(t, err)

	tDef, err := events.NewTimerEventDefinition(timeExpr, nil, nil)
	require.NoError(t, err)

	tr := &track{instance: &Instance{EngineRuntime: enginert.Default()}}
	tr.instance.now = tr.instance.Clock().Now

	tr.stashTimerPlan(tDef, nil)
	require.True(t, when.Equal(tr.timerDeadline),
		"the absolute plan must stash at arming")

	fresh := &track{}
	fresh.stashTimerPlan(nil, nil)
	require.True(t, fresh.timerDeadline.IsZero(),
		"a nil definition stashes nothing")
}

// TestInstanceReportExported: the exported Report wrapper reaches the
// standard emission point.
func TestInstanceReportExported(t *testing.T) {
	sink := &cpSink{}
	rt := enginert.Default().WithReporter(sink)

	inst := &Instance{EngineRuntime: rt}
	inst.Report(observability.Fact{
		Kind:  observability.KindInstanceState,
		Phase: observability.PhaseRecovered,
	})

	require.True(t, sink.has(observability.PhaseRecovered))
}

// TestRestoreDataArms: garbage scope data and empty records exercise
// the remaining restore relays.
func TestRestoreDataArms(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	_, _, doc, cancel := parkAndInspect(t)
	cancel()

	t.Run("garbage scope data is loud",
		func(t *testing.T) {
			s2 := condSnapshotFor(t, doc)

			broken := *doc
			broken.Tracks = nil
			broken.Scopes = []checkpoint.ScopeRecord{{
				Path: doc.Scopes[0].Path,
				Data: []byte("not json"),
			}}

			ep := mockeventproc.NewMockEventProducer(t)

			_, err := Restore(&broken, s2, scope.EmptyDataPath,
				enginert.Default(), ep, nil, nil)
			require.Error(t, err)
		})

	t.Run("an empty scope record just reopens",
		func(t *testing.T) {
			s2 := condSnapshotFor(t, doc)

			slim := *doc
			slim.Tracks = nil
			slim.Scopes = []checkpoint.ScopeRecord{
				{Path: doc.Scopes[0].Path},
				{Path: doc.Scopes[0].Path + "/sp-empty"},
			}

			ep := mockeventproc.NewMockEventProducer(t)

			restored, err := Restore(&slim, s2, scope.EmptyDataPath,
				enginert.Default(), ep, nil, nil)
			require.NoError(t, err)

			_, err = restored.sc.plane.OwnData(
				scope.DataPath(doc.Scopes[0].Path + "/sp-empty"))
			require.NoError(t, err)
		})

	t.Run("garbage ledger snapshot is loud",
		func(t *testing.T) {
			s2 := condSnapshotFor(t, doc)

			broken := *doc
			broken.Tracks = nil
			broken.Ledgers = []checkpoint.LedgerRecord{{
				ScopePath:  doc.Scopes[0].Path,
				ActivityID: "x",
				Snapshot:   []byte("boom"),
			}}

			ep := mockeventproc.NewMockEventProducer(t)

			_, err := Restore(&broken, s2, scope.EmptyDataPath,
				enginert.Default(), ep, nil, nil)
			require.Error(t, err)
		})
}

// TestRestoreKeysAndLedgerEncodeArms: the conversation-key restore and
// the capture-side ledger encode guard.
func TestRestoreKeysAndLedgerEncodeArms(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	t.Run("conversation keys restore",
		func(t *testing.T) {
			c := &correlator{keys: map[string]string{}}
			c.restoreKeys(map[string]string{"orderID": "42"})
			c.restoreKeys(nil) // the empty guard

			require.Equal(t, "42", c.keys["orderID"])
		})

	t.Run("an unencodable ledger snapshot defers the capture",
		func(t *testing.T) {
			val := false
			evals := 0

			def, err := events.NewConditionalEventDefinition(
				condExpr(t, &val, &evals))
			require.NoError(t, err)

			rt := cpRuntime(t)
			ep := mockeventproc.NewMockEventProducer(t)

			inst, err := New(condSnapshot(t, def), scope.EmptyDataPath,
				rt, ep, nil,
				WithCheckpointing("engine-A", "engine-A", time.Minute))
			require.NoError(t, err)

			bad, err := data.ReadyValueParameter("hot",
				values.NewVariable(make(chan int)))
			require.NoError(t, err)

			ls := newLoopState(inst)
			ls.ledgers[inst.sc.root] = []*ledgerEntry{{
				activityID: "x",
				snapshot:   []data.Data{bad},
			}}

			_, reason := ls.captureDocument(context.Background())
			require.Contains(t, reason, "ledger encode:")
		})
}

// firstTrackID returns any track id of the instance (the armed-capture
// tests need a host for a synthetic group).
func firstTrackID(inst *Instance) string {
	for id := range inst.tracks {
		return id
	}

	return ""
}
