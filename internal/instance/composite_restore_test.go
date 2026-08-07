package instance

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// SRD-082 M1 (T-2/T-3/T-4): schema-3 compatibility, the composite
// double-execution fix and sequential iteration restore-at-position.
//
// The parks are GATED CONDITIONALS: the body's catch fires at arming
// while the gate allows the pass, and parks otherwise — so a capture
// lands at a deterministic position, and a restored catch re-evaluates
// at re-arming (the recovery contract) against the reopened gate.

// gatedCond is a conditional definition true iff gate > floor(lc),
// where lc is the bound loopCounter (0 when none is in scope).
func gatedCond(
	t *testing.T, gate *atomic.Int32,
) *events.ConditionalEventDefinition {
	t.Helper()

	expr := goexpr.Must(nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			lc := 0
			if d, err := ds.Find(ctx, "loopCounter"); err == nil {
				if v, ok := d.Value().Get(ctx).(int); ok {
					lc = v
				}
			}

			return values.NewVariable(int(gate.Load()) > lc), nil
		})

	def, err := events.NewConditionalEventDefinition(expr)
	require.NoError(t, err)

	return def
}

// gatedBodyProcess builds start → body(SubProcess[, loop lch]) → end
// with the body b-start → work(op) → catch(gated) → b-end, every
// node id PINNED so one snapshot serves capture and restore.
func gatedBodyProcess(
	t *testing.T, key string, op service.Operation, gate *atomic.Int32,
	lch activities.LoopCharacteristics, props ...*data.Property,
) *snapshot.Snapshot {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New(key, foundation.WithID(key),
		data.WithProperties(props...))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start",
		foundation.WithID(key+"-start"))
	require.NoError(t, err)

	spOpts := []options.Option{foundation.WithID(key + "-body")}
	if lch != nil {
		spOpts = append(spOpts, activities.WithLoop(lch))
	}

	body, err := activities.NewSubProcess("body", spOpts...)
	require.NoError(t, err)

	bStart, err := events.NewStartEvent("b-start",
		foundation.WithID(key+"-b-start"))
	require.NoError(t, err)

	work, err := activities.NewServiceTask("work", op,
		activities.WithoutParams(), foundation.WithID(key+"-work"))
	require.NoError(t, err)

	catch, err := events.NewIntermediateCatchEvent("b-catch",
		gatedCond(t, gate), foundation.WithID(key+"-b-catch"))
	require.NoError(t, err)

	bEnd, err := events.NewEndEvent("b-end",
		foundation.WithID(key+"-b-end"))
	require.NoError(t, err)

	for _, e := range []flow.Element{bStart, work, catch, bEnd} {
		require.NoError(t, body.Add(e))
	}

	link(t, bStart, work)
	link(t, work, catch)
	link(t, catch, bEnd)

	end, err := events.NewEndEvent("end", foundation.WithID(key+"-end"))
	require.NoError(t, err)

	for _, e := range []flow.Element{start, body, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, body)
	link(t, body, end)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	return s
}

// captureAt runs an armed instance until the repository holds a
// checkpoint matching want, returns a deep copy of that document, and
// abandons the run (cancel without waiting — the crash shape).
func captureAt(
	t *testing.T, s *snapshot.Snapshot,
	want func(*checkpoint.Document) bool,
) *checkpoint.Document {
	t.Helper()

	rt := cpRuntime(t)
	ep := mockeventproc.NewMockEventProducer(t)
	ep.EXPECT().RegisterEvent(mock.Anything, mock.Anything).
		Return(nil).Maybe()

	inst, err := New(s, scope.EmptyDataPath, rt, ep, nil,
		WithCheckpointing("engine-A", "engine-A", time.Minute))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	require.NoError(t, inst.Run(ctx))

	var doc, last *checkpoint.Document

	if !assert.Eventually(t, func() bool {
		rec, ok, err := rt.Repository().Load(context.Background(), inst.ID())
		if err != nil || !ok {
			return false
		}

		d, err := checkpoint.Unmarshal(rec.Payload)
		if err != nil {
			return false
		}

		last = d
		if !want(d) {
			return false
		}

		doc = d

		return true
	}, 3*time.Second, 2*time.Millisecond) {
		if last != nil {
			for _, tr := range last.Tracks {
				t.Logf("last doc track: node=%s state=%s mi=%+v",
					tr.NodeID, tr.State, tr.MI)
			}

			for _, sc := range last.Scopes {
				t.Logf("last doc scope: %s", sc.Path)
			}
		}

		t.Fatal("the wanted checkpoint never appeared")
	}

	cancel() // abandon; the captured copy is the restore source

	return doc
}

// restoreToDone restores doc over the SAME snapshot and runs the
// instance to completion.
func restoreToDone(
	t *testing.T, doc *checkpoint.Document, s *snapshot.Snapshot,
) *Instance {
	t.Helper()

	ep := mockeventproc.NewMockEventProducer(t)
	ep.EXPECT().RegisterEvent(mock.Anything, mock.Anything).
		Return(nil).Maybe()

	restored, err := Restore(doc, s, scope.EmptyDataPath,
		cpRuntime(t), ep, nil, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	require.NoError(t, restored.Run(ctx))

	select {
	case <-restored.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("the restored instance did not finish")
	}

	require.Equal(t, Completed, restored.State())

	return restored
}

// openScope reports whether the document holds an open non-root scope
// (the composite body is in flight) — the root is the single-segment
// process path, so depth means a second separator.
func openScope(d *checkpoint.Document) bool {
	for _, sc := range d.Scopes {
		if strings.Count(sc.Path, "/") > 1 {
			return true
		}
	}

	return false
}

// hostMI returns the recorded iteration position of the body host, or
// nil.
func hostMI(d *checkpoint.Document, key string) *checkpoint.MIRecord {
	for i := range d.Tracks {
		if d.Tracks[i].NodeID == key+"-body" {
			return d.Tracks[i].MI
		}
	}

	return nil
}

// TestCompositeRestoreRunsBodyOnce is the T-3 double-execution
// regression: a plain composite captured mid-body restores with the
// drained scope resuming its host exactly once — the body's work is
// never re-executed.
func TestCompositeRestoreRunsBodyOnce(t *testing.T) {
	var count, gate atomic.Int32 // gate 0: the catch parks after work ran

	s := gatedBodyProcess(t, "cr-plain", countOp(t, &count), &gate, nil)

	doc := captureAt(t, s, func(d *checkpoint.Document) bool {
		return openScope(d) && len(d.Tracks) == 2 // host + parked catch
	})

	require.Equal(t, int32(1), count.Load(), "one body pass before the crash")

	gate.Store(1) // reopen the gate; the restored catch fires at re-arming
	restoreToDone(t, doc, s)

	require.Equal(t, int32(1), count.Load(),
		"the restored body must NOT re-execute (FR-5)")
}

// TestSequentialMIRestoresAtPass is T-4's MI half: captured with pass 1
// parked, the restored decorator resumes AT pass 1 — pass 0 never
// re-runs, its staged output survives, and the assembled output is
// complete.
func TestSequentialMIRestoresAtPass(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var count, gate atomic.Int32

	gate.Store(1) // pass 0 flows through; pass 1 parks at its catch

	mi, err := activities.NewMultiInstance(
		activities.WithSequential(),
		activities.WithInputCollection("items", "item"),
		activities.WithOutputCollection("outs", "res"))
	require.NoError(t, err)

	items := data.MustProperty("items",
		data.MustItemDefinition(values.NewArray("a", "b", "c"),
			foundation.WithID("items")),
		data.ReadyDataState)

	// the body reads its input item and writes the output item the
	// sequential capture stages at each drain.
	op, err := gooper.New("cr-mi-op",
		func(ctx context.Context, r service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			count.Add(1)

			d, err := r.GetData("item")
			if err != nil {
				return nil, err
			}

			v, _ := d.Value().Get(ctx).(string)

			return data.MustItemDefinition(
				values.NewVariable("R:"+v), foundation.WithID("res")), nil
		})
	require.NoError(t, err)

	s := gatedBodyProcess(t, "cr-mi", op, &gate, mi, items)

	doc := captureAt(t, s, func(d *checkpoint.Document) bool {
		rec := hostMI(d, "cr-mi")

		return rec != nil && rec.Completed == 1 && openScope(d)
	})

	require.Equal(t, int32(2), count.Load(),
		"passes 0 and 1 each ran their work before the crash")

	rec := hostMI(doc, "cr-mi")
	require.Equal(t, 3, rec.N, "the frozen activation count rides the record")
	require.NotEmpty(t, rec.Staging, "pass 0's staged output rides the record")

	gate.Store(3) // open every remaining pass
	restored := restoreToDone(t, doc, s)

	require.Equal(t, int32(3), count.Load(),
		"only pass 2's work runs after the restore — nothing re-executes")

	d, err := restored.sc.plane.GetData(restored.sc.root, "outs")
	require.NoError(t, err)

	col, ok := d.Value().(data.Collection)
	require.True(t, ok)
	require.Equal(t, []any{"R:a", "R:b", "R:c"},
		col.GetAll(context.Background()),
		"the assembled output holds every pass — the restored pass-0 "+
			"element uniform with the live ones")
}

// TestStandardLoopRestoresAtPass is T-4's Standard-Loop half: the
// restored composite loop resumes at the recorded pass.
func TestStandardLoopRestoresAtPass(t *testing.T) {
	var count, gate atomic.Int32

	gate.Store(1) // pass 0 through; pass 1 parks

	sl, err := activities.NewStandardLoop(loopCondLt(t, 3))
	require.NoError(t, err)

	s := gatedBodyProcess(t, "cr-sl", countOp(t, &count), &gate, sl)

	doc := captureAt(t, s, func(d *checkpoint.Document) bool {
		rec := hostMI(d, "cr-sl")

		return rec != nil && rec.Completed == 1 && openScope(d)
	})

	require.Equal(t, int32(2), count.Load())

	gate.Store(10)
	restoreToDone(t, doc, s)

	require.Equal(t, int32(3), count.Load(),
		"3 passes total across the crash — none re-executed")
}

// TestSchemaThreeDocumentRestores is T-2: a pre-fidelity (schema 3)
// document — no position records — restores exactly as today.
func TestSchemaThreeDocumentRestores(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	_, inst, doc, cancel := parkAndInspect(t)
	cancel()

	raw, err := doc.Marshal()
	require.NoError(t, err)

	// rewrite the wire form to schema 3 — the shape every pre-SRD-082
	// engine wrote (its guards ensured no construct was in flight).
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	m["schema"] = 3
	delete(m, "calls")
	delete(m, "mi_groups")
	delete(m, "sweeps")

	old, err := json.Marshal(m)
	require.NoError(t, err)

	back, err := checkpoint.Unmarshal(old)
	require.NoError(t, err)
	require.Equal(t, 3, back.Schema)

	s2 := condSnapshotFor(t, back)

	ep := mockeventproc.NewMockEventProducer(t)
	ep.EXPECT().RegisterEvent(mock.Anything, mock.Anything).
		Return(nil).Maybe()

	restored, err := Restore(back, s2, scope.EmptyDataPath,
		cpRuntime(t), ep, nil, nil)
	require.NoError(t, err)
	require.Equal(t, inst.ID(), restored.ID())
	require.Len(t, restored.tracks, 1,
		"the parked track rebuilds; absent records rebuild nothing")
}

// miCapturedDoc runs the T-4 scenario to its pass-1 park and returns
// the captured document plus the snapshot and counters for restores.
func miCapturedDoc(
	t *testing.T, key string, cond data.FormalExpression,
) (*checkpoint.Document, *snapshot.Snapshot, *atomic.Int32, *atomic.Int32) {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	var count, gate atomic.Int32

	gate.Store(1)

	opts := []activities.MultiInstanceOption{
		activities.WithSequential(),
		activities.WithInputCollection("items", "item"),
		activities.WithOutputCollection("outs", "res"),
	}
	if cond != nil {
		opts = append(opts, activities.WithCompletionCondition(cond))
	}

	mi, err := activities.NewMultiInstance(opts...)
	require.NoError(t, err)

	items := data.MustProperty("items",
		data.MustItemDefinition(values.NewArray("a", "b", "c"),
			foundation.WithID("items")),
		data.ReadyDataState)

	op, err := gooper.New(key+"-op",
		func(ctx context.Context, r service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			count.Add(1)

			d, err := r.GetData("item")
			if err != nil {
				return nil, err
			}

			v, _ := d.Value().Get(ctx).(string)

			return data.MustItemDefinition(
				values.NewVariable("R:"+v), foundation.WithID("res")), nil
		})
	require.NoError(t, err)

	s := gatedBodyProcess(t, key, op, &gate, mi, items)

	doc := captureAt(t, s, func(d *checkpoint.Document) bool {
		rec := hostMI(d, key)

		return rec != nil && rec.Completed == 1 && openScope(d)
	})

	return doc, s, &count, &gate
}

// TestSequentialMIRestoreHonorsRecordedStop: a recorded ConditionMet
// ends the iteration at restore — completed passes stand, nothing else
// launches (SRD-082 FR-3).
func TestSequentialMIRestoreHonorsRecordedStop(t *testing.T) {
	doc, s, count, gate := miCapturedDoc(t, "cr-mistop", nil)

	// what the runner's scopeNote would have recorded had the condition
	// fired at pass 0's drain.
	rec := hostMI(doc, "cr-mistop")
	rec.ConditionMet = true

	// drop the in-flight pass-1 state: the stop stands ahead of it —
	// the capture the note precedes carries only the completed passes.
	trimToCompletedPasses(doc, "cr-mistop")

	gate.Store(3)
	restored := restoreToDone(t, doc, s)

	require.Equal(t, int32(2), count.Load(),
		"no further pass launches after the recorded stop")

	d, err := restored.sc.plane.GetData(restored.sc.root, "outs")
	require.NoError(t, err)

	col, _ := d.Value().(data.Collection)
	require.Equal(t, []any{"R:a"}, col.GetAll(context.Background()),
		"the published output is the completed passes' staging")
}

// TestSequentialMIRestoreReevaluatesCondition: when the capture
// predates the runner's note, the seeded runner re-evaluates the SAME
// predicate over the restored data — a true verdict stops the
// iteration without launching further passes (SRD-082 FR-3).
func TestSequentialMIRestoreReevaluatesCondition(t *testing.T) {
	var stopNow atomic.Bool

	cond := goexpr.Must(nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(_ context.Context, _ data.Source) (data.Value, error) {
			return values.NewVariable(stopNow.Load()), nil
		})

	doc, s, count, gate := miCapturedDoc(t, "cr-mireval", cond)

	trimToCompletedPasses(doc, "cr-mireval")

	stopNow.Store(true) // the restored predicate now holds
	gate.Store(3)
	restored := restoreToDone(t, doc, s)

	require.Equal(t, int32(2), count.Load(),
		"the re-evaluated condition stops the iteration at the seed")

	d, err := restored.sc.plane.GetData(restored.sc.root, "outs")
	require.NoError(t, err)

	col, _ := d.Value().(data.Collection)
	require.Equal(t, []any{"R:a"}, col.GetAll(context.Background()))
}

// trimToCompletedPasses drops the in-flight pass from a captured MI
// document: the open body scope and its inner tracks — the shape a
// capture between passes writes.
func trimToCompletedPasses(doc *checkpoint.Document, key string) {
	scopes := doc.Scopes[:0]

	for _, sc := range doc.Scopes {
		if strings.Count(sc.Path, "/") <= 1 {
			scopes = append(scopes, sc)
		}
	}

	doc.Scopes = scopes

	tracks := doc.Tracks[:0]

	for _, tr := range doc.Tracks {
		if tr.NodeID == key+"-body" {
			tracks = append(tracks, tr)
		}
	}

	doc.Tracks = tracks
}

// TestRestoredScopeWithoutHostFailsLoud: a document whose scope and
// track tables disagree — an open composite scope with no host track —
// fails the restore loudly (SRD-082 FR-5, NFR-2).
func TestRestoredScopeWithoutHostFailsLoud(t *testing.T) {
	var count, gate atomic.Int32

	s := gatedBodyProcess(t, "cr-nohost", countOp(t, &count), &gate, nil)

	doc := captureAt(t, s, func(d *checkpoint.Document) bool {
		return openScope(d) && len(d.Tracks) == 2
	})

	// corrupt: drop the HOST track, keep its open scope.
	tracks := doc.Tracks[:0]

	for _, tr := range doc.Tracks {
		if tr.NodeID != "cr-nohost-body" {
			tracks = append(tracks, tr)
		}
	}

	doc.Tracks = tracks

	ep := mockeventproc.NewMockEventProducer(t)
	ep.EXPECT().RegisterEvent(mock.Anything, mock.Anything).
		Return(nil).Maybe()

	restored, err := Restore(doc, s, scope.EmptyDataPath,
		cpRuntime(t), ep, nil, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	require.NoError(t, restored.Run(ctx))

	// fail() is phase-only: State() reports Terminated while the loud
	// half is the InstanceState/Failed fact (ADR-022 §2.4).
	require.Eventually(t, func() bool {
		return restored.State() == Terminated
	}, 3*time.Second, 5*time.Millisecond,
		"the adoption must fail the instance loud, never run it wrong")
}

// TestIncidentPinHoldsRestoredScope: a restored open incident inside a
// composite scope keeps holding it (SRD-079 §3.2 over SRD-082 FR-5) —
// the scope never drains past the pin, so the host stays parked.
func TestIncidentPinHoldsRestoredScope(t *testing.T) {
	var count, gate atomic.Int32

	s := gatedBodyProcess(t, "cr-pin", countOp(t, &count), &gate, nil)

	doc := captureAt(t, s, func(d *checkpoint.Document) bool {
		return openScope(d) && len(d.Tracks) == 2
	})

	var bodyPath string

	for _, sc := range doc.Scopes {
		if strings.Count(sc.Path, "/") > 1 {
			bodyPath = sc.Path
		}
	}

	doc.Incidents = append(doc.Incidents, checkpoint.IncidentRecord{
		FirstAt:   time.Now(),
		LastAt:    time.Now(),
		ID:        "inc-1",
		NodeID:    "cr-pin-work",
		TrackID:   "gone-track",
		ScopePath: bodyPath,
		Cause:     "synthetic",
		State:     "open",
		Attempts:  1,
	})

	ep := mockeventproc.NewMockEventProducer(t)
	ep.EXPECT().RegisterEvent(mock.Anything, mock.Anything).
		Return(nil).Maybe()

	restored, err := Restore(doc, s, scope.EmptyDataPath,
		cpRuntime(t), ep, nil, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	gate.Store(1) // the catch fires; only the incident pin remains

	require.NoError(t, restored.Run(ctx))

	// the body's live track drains, the pin holds: never Completed.
	require.Never(t, func() bool {
		return restored.State() == Completed
	}, 400*time.Millisecond, 20*time.Millisecond,
		"an open incident's pin must survive the restore")
}

// TestSeedStagingRefusals: the staging seed refuses garbage and a
// non-collection encoding loud (SRD-082 FR-3, NFR-2).
func TestSeedStagingRefusals(t *testing.T) {
	ctx := t.Context()
	staging := values.NewArray[any]()

	require.Error(t,
		seedStaging(ctx, staging, json.RawMessage(`{broken`)),
		"undecodable staging must refuse")

	scalar, err := checkpoint.EncodeValue(ctx, "test",
		values.NewVariable("not-a-collection"))
	require.NoError(t, err)

	err = seedStaging(ctx, staging, scalar)
	require.Error(t, err)
	require.Contains(t, err.Error(), "isn't a collection")
}

// TestRestoredScopeHostSegOverride: a host carrying a per-fire scope
// segment (the Event Sub-Process convention) resolves through its
// override, not the node-derived segment.
func TestRestoredScopeHostSegOverride(t *testing.T) {
	var count, gate atomic.Int32

	s := gatedBodyProcess(t, "cr-seg", countOp(t, &count), &gate, nil)

	body, ok := s.NodeByID("cr-seg-body")
	require.True(t, ok)

	host := &track{
		steps:     []*stepInfo{{node: body}},
		scopePath: scope.DataPath("/cr-seg"),
		scopeSeg:  "fire-7",
	}

	child, err := host.scopePath.Append("fire-7")
	require.NoError(t, err)

	got, node := restoredScopeHost([]*track{host}, host.scopePath, child)
	require.Same(t, host, got)
	require.Equal(t, "cr-seg-body", node.ID())

	// the node-derived segment no longer matches under the override.
	plain, err := host.scopePath.Append("sp-cr-seg-body")
	require.NoError(t, err)

	miss, _ := restoredScopeHost([]*track{host}, host.scopePath, plain)
	require.Nil(t, miss)
}

// restoreExpectFault restores doc and runs it, requiring the instance
// to finish NOT Completed — the loud-refusal shape of a restored
// document the seeded decorator cannot honor (SRD-082 NFR-2).
func restoreExpectFault(
	t *testing.T, doc *checkpoint.Document, s *snapshot.Snapshot,
) {
	t.Helper()

	ep := mockeventproc.NewMockEventProducer(t)
	ep.EXPECT().RegisterEvent(mock.Anything, mock.Anything).
		Return(nil).Maybe()

	restored, err := Restore(doc, s, scope.EmptyDataPath,
		cpRuntime(t), ep, nil, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	require.NoError(t, restored.Run(ctx))

	select {
	case <-restored.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("the faulted instance did not finish")
	}

	require.NotEqual(t, Completed, restored.State())
}

// TestSequentialMIRestoreRefusals: a restored position the live data
// cannot honor refuses loud — a frozen N beyond the collection,
// garbage staging, an erroring re-evaluated condition (SRD-082 FR-3).
func TestSequentialMIRestoreRefusals(t *testing.T) {
	t.Run("a frozen N beyond the input collection", func(t *testing.T) {
		doc, s, _, gate := miCapturedDoc(t, "cr-mibadn", nil)

		hostMI(doc, "cr-mibadn").N = 5 // the collection holds 3

		gate.Store(5)
		restoreExpectFault(t, doc, s)
	})

	t.Run("garbage staging", func(t *testing.T) {
		doc, s, _, gate := miCapturedDoc(t, "cr-mibadstage", nil)

		hostMI(doc, "cr-mibadstage").Staging = json.RawMessage(`{broken`)

		gate.Store(3)
		restoreExpectFault(t, doc, s)
	})

	t.Run("an erroring re-evaluated condition", func(t *testing.T) {
		var boomNow atomic.Bool

		boom := goexpr.Must(nil,
			data.MustItemDefinition(values.NewVariable(false)),
			func(_ context.Context, _ data.Source) (data.Value, error) {
				if boomNow.Load() {
					return nil, errs.New(errs.M("cond boom"),
						errs.C(errorClass, errs.OperationFailed))
				}

				return values.NewVariable(false), nil
			})

		doc, s, _, gate := miCapturedDoc(t, "cr-miboom", boom)

		boomNow.Store(true) // healthy during the run, erroring at restore

		trimToCompletedPasses(doc, "cr-miboom")

		gate.Store(3)
		restoreExpectFault(t, doc, s)
	})
}

// TestSequentialMIRestoreAtPassZero: a capture parked in pass 0 seeds
// completed=0 — the whole iteration runs post-restore, nothing twice.
func TestSequentialMIRestoreAtPassZero(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var count, gate atomic.Int32 // gate 0: pass 0 parks at its catch

	mi, err := activities.NewMultiInstance(
		activities.WithSequential(),
		activities.WithInputCollection("items", "item"))
	require.NoError(t, err)

	items := data.MustProperty("items",
		data.MustItemDefinition(values.NewArray("a", "b", "c"),
			foundation.WithID("items")),
		data.ReadyDataState)

	s := gatedBodyProcess(t, "cr-mi0", countOp(t, &count), &gate, mi, items)

	doc := captureAt(t, s, func(d *checkpoint.Document) bool {
		rec := hostMI(d, "cr-mi0")

		return rec != nil && rec.Completed == 0 && openScope(d)
	})

	require.Equal(t, int32(1), count.Load(), "pass 0's work ran pre-crash")

	gate.Store(3)
	restoreToDone(t, doc, s)

	require.Equal(t, int32(3), count.Load(),
		"passes 1 and 2 run once each; pass 0 never re-executes")
}

// TestCaptureDefersOnUncodableStaging: an unserializable staged output
// defers the checkpoint loudly at capture (ADR-033 §2.1.3) instead of
// writing a torn record.
func TestCaptureDefersOnUncodableStaging(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var gate atomic.Int32

	gate.Store(1) // pass 0 through (staging one chan), pass 1 parks

	mi, err := activities.NewMultiInstance(
		activities.WithSequential(),
		activities.WithInputCollection("items", "item"),
		activities.WithOutputCollection("outs", "res"))
	require.NoError(t, err)

	items := data.MustProperty("items",
		data.MustItemDefinition(values.NewArray("a", "b"),
			foundation.WithID("items")),
		data.ReadyDataState)

	op, err := gooper.New("cr-hot-op",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			return data.MustItemDefinition(
				values.NewVariable(make(chan int)),
				foundation.WithID("res")), nil
		})
	require.NoError(t, err)

	s := gatedBodyProcess(t, "cr-hot", op, &gate, mi, items)

	rt := cpRuntime(t)
	sink := &cpSink{}
	ep := mockeventproc.NewMockEventProducer(t)
	ep.EXPECT().RegisterEvent(mock.Anything, mock.Anything).
		Return(nil).Maybe()

	inst, err := New(s, scope.EmptyDataPath, rt.WithReporter(sink), ep, nil,
		WithCheckpointing("engine-A", "engine-A", time.Minute))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	require.NoError(t, inst.Run(ctx))

	require.Eventually(t, func() bool {
		return sink.has(observability.PhaseCheckpointDeferred)
	}, 3*time.Second, 5*time.Millisecond,
		"the uncodable staging must defer the capture loudly")
}
