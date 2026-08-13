package instance

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
)

// SRD-086 M1 — the sequential leaf Multi-Instance runs IN PLACE:
// before runLeafMISequential a leaf MI silently ran once (the §1
// probe), whatever the collection said.

// leafMISnapshot builds start → work(leaf ServiceTask under MI) → end.
// The op records "<loopCounter>:<item>" per pass and returns the item
// prefixed "R:" as the pass output.
func leafMISnapshot(
	t *testing.T, key string, mu *sync.Mutex, log *[]string,
	miOpts ...activities.MultiInstanceOption,
) *snapshot.Snapshot {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	opts := append([]activities.MultiInstanceOption{
		activities.WithSequential(),
		activities.WithInputCollection("items", "item"),
		activities.WithOutputCollection("outs", "res"),
	}, miOpts...)

	mi, err := activities.NewMultiInstance(opts...)
	require.NoError(t, err)

	items := data.MustProperty("items",
		data.MustItemDefinition(values.NewArray("a", "b", "c"),
			foundation.WithID("items")),
		data.ReadyDataState)

	outs := data.MustProperty("outs",
		data.MustItemDefinition(values.NewArray[any](),
			foundation.WithID("outs")),
		data.ReadyDataState)

	op, err := gooper.New(key+"-op",
		func(ctx context.Context, r service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			item, err := r.GetData("item")
			if err != nil {
				return nil, err
			}

			lc, err := r.GetData("loopCounter")
			if err != nil {
				return nil, err
			}

			mu.Lock()
			*log = append(*log, fmt.Sprintf("%v:%v",
				lc.Value().Get(ctx), item.Value().Get(ctx)))
			mu.Unlock()

			return data.MustItemDefinition(
				values.NewVariable("R:"+fmt.Sprint(item.Value().Get(ctx))),
				foundation.WithID("res")), nil
		})
	require.NoError(t, err)

	p, err := process.New(key, foundation.WithID(key),
		data.WithProperties(items, outs))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start",
		foundation.WithID(key+"-start"))
	require.NoError(t, err)

	work, err := activities.NewServiceTask("work", op,
		activities.WithoutParams(), activities.WithLoop(mi),
		foundation.WithID(key+"-work"))
	require.NoError(t, err)

	end, err := events.NewEndEvent("end", foundation.WithID(key+"-end"))
	require.NoError(t, err)

	for _, e := range []flow.Element{start, work, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, work)
	link(t, work, end)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	return s
}

// runLeafMI runs the snapshot to completion and returns the instance.
func runLeafMI(t *testing.T, s *snapshot.Snapshot) *Instance {
	t.Helper()

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(),
		&recordingProducer{}, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, inst.Run(ctx))

	select {
	case <-inst.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("the leaf MI did not finish")
	}

	require.Equal(t, Completed, inst.State())

	return inst
}

// TestLeafMISequentialRunsAllPasses is T-1 + T-8 (SRD-086 FR-1): the
// §1 probe inverted — 3 items now mean 3 passes, in order, each seeing
// ITS item and loopCounter, with the output assembled in order.
func TestLeafMISequentialRunsAllPasses(t *testing.T) {
	var (
		mu  sync.Mutex
		log []string
	)

	s := leafMISnapshot(t, "lm-seq", &mu, &log)
	inst := runLeafMI(t, s)

	mu.Lock()
	require.Equal(t, []string{"0:a", "1:b", "2:c"}, log,
		"3 items must mean 3 in-order passes — the silent single-run pin")
	mu.Unlock()

	outs, err := inst.sc.plane.GetData(
		scope.DataPath("/lm-seq"), "outs")
	require.NoError(t, err)

	col, ok := outs.Value().(data.Collection)
	require.True(t, ok)

	got := make([]any, 0, col.Count())
	for _, el := range col.GetAll(context.Background()) {
		got = append(got, el)
	}

	require.Equal(t, []any{"R:a", "R:b", "R:c"}, got,
		"the assembled output holds one slot per pass, in order")
}

// TestLeafMISequentialCompletionStops is T-2 (SRD-086 FR-1): a true
// completionCondition stops the passes early; completed outputs stand.
func TestLeafMISequentialCompletionStops(t *testing.T) {
	var (
		mu  sync.Mutex
		log []string
	)

	cond := goexpr.Must(nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			d, err := ds.Find(ctx, "numberOfCompletedInstances")
			if err != nil {
				return nil, err
			}

			n, _ := d.Value().Get(ctx).(int)

			// evaluated with PASS-START counts (the SRD-055 family
			// semantics): true once at least one pass completed —
			// which the evaluation after pass 2 sees.
			return values.NewVariable(n >= 1), nil
		})

	s := leafMISnapshot(t, "lm-stop", &mu, &log,
		activities.WithCompletionCondition(cond))
	runLeafMI(t, s)

	mu.Lock()
	require.Equal(t, []string{"0:a", "1:b"}, log,
		"a true condition after pass 2 must stop pass 3")
	mu.Unlock()
}

// TestLeafMISequentialZeroItems: N == 0 runs zero passes and the token
// still leaves via the declared outgoing flow (SRD-086 FR-1).
func TestLeafMISequentialZeroItems(t *testing.T) {
	var (
		mu  sync.Mutex
		log []string
	)

	require.NoError(t, data.CreateDefaultStates())

	mi, err := activities.NewMultiInstance(
		activities.WithSequential(),
		activities.WithInputCollection("items", "item"))
	require.NoError(t, err)

	items := data.MustProperty("items",
		data.MustItemDefinition(values.NewArray[any](),
			foundation.WithID("items")),
		data.ReadyDataState)

	op, err := gooper.New("lm-zero-op",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			mu.Lock()
			log = append(log, "ran")
			mu.Unlock()

			return nil, nil
		})
	require.NoError(t, err)

	p, err := process.New("lm-zero", foundation.WithID("lm-zero"),
		data.WithProperties(items))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	work, err := activities.NewServiceTask("work", op,
		activities.WithoutParams(), activities.WithLoop(mi))
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, work, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, work)
	link(t, work, end)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	runLeafMI(t, s)

	mu.Lock()
	require.Empty(t, log, "zero items must mean zero passes")
	mu.Unlock()
}

// TestLeafMISequentialNoOutput: an MI that assembles no output skips
// staging entirely (captureLeafOutput's nil branch).
func TestLeafMISequentialNoOutput(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var (
		mu  sync.Mutex
		log []string
	)

	mi, err := activities.NewMultiInstance(
		activities.WithSequential(),
		activities.WithInputCollection("items", "item"))
	require.NoError(t, err)

	items := data.MustProperty("items",
		data.MustItemDefinition(values.NewArray("x", "y"),
			foundation.WithID("items")),
		data.ReadyDataState)

	op, err := gooper.New("lm-noout-op",
		func(ctx context.Context, r service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			item, err := r.GetData("item")
			if err != nil {
				return nil, err
			}

			mu.Lock()
			log = append(log, fmt.Sprint(item.Value().Get(ctx)))
			mu.Unlock()

			return nil, nil
		})
	require.NoError(t, err)

	p, err := process.New("lm-noout", foundation.WithID("lm-noout"),
		data.WithProperties(items))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	work, err := activities.NewServiceTask("work", op,
		activities.WithoutParams(), activities.WithLoop(mi))
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, work, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, work)
	link(t, work, end)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	runLeafMI(t, s)

	mu.Lock()
	require.Equal(t, []string{"x", "y"}, log)
	mu.Unlock()
}

// TestLeafMISequentialOpFailure: a failing pass faults the run — the
// error propagates out of the decorator (SRD-086 FR-1).
func TestLeafMISequentialOpFailure(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	mi, err := activities.NewMultiInstance(
		activities.WithSequential(),
		activities.WithInputCollection("items", "item"))
	require.NoError(t, err)

	items := data.MustProperty("items",
		data.MustItemDefinition(values.NewArray("x", "y"),
			foundation.WithID("items")),
		data.ReadyDataState)

	op, err := gooper.New("lm-fail-op",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			return nil, fmt.Errorf("pass boom")
		})
	require.NoError(t, err)

	p, err := process.New("lm-fail", foundation.WithID("lm-fail"),
		data.WithProperties(items))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	work, err := activities.NewServiceTask("work", op,
		activities.WithoutParams(), activities.WithLoop(mi))
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, work, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, work)
	link(t, work, end)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(),
		&recordingProducer{}, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, inst.Run(ctx))

	select {
	case <-inst.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("the failing leaf MI did not finish")
	}

	require.NotEqual(t, Completed, inst.State(),
		"a failing pass must not complete the run")
}

// TestLeafMISequentialCondFailure: a failing completionCondition is a
// modeling error that faults the run (SRD-086 FR-1).
func TestLeafMISequentialCondFailure(t *testing.T) {
	var (
		mu  sync.Mutex
		log []string
	)

	cond := goexpr.Must(nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(_ context.Context, _ data.Source) (data.Value, error) {
			return nil, fmt.Errorf("cond boom")
		})

	s := leafMISnapshot(t, "lm-cfail", &mu, &log,
		activities.WithCompletionCondition(cond))

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(),
		&recordingProducer{}, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, inst.Run(ctx))

	select {
	case <-inst.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("the failing-condition leaf MI did not finish")
	}

	require.NotEqual(t, Completed, inst.State())
}

// TestLeafMIParallelFansOut is T-3 (SRD-086 FR-2/FR-3): 3 items run
// CONCURRENTLY — each op parks until all three have arrived, so a
// sequential execution would deadlock — each reads ITS item, the
// outputs land by ordinal, and the flow follows once.
func TestLeafMIParallelFansOut(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var (
		mu      sync.Mutex
		log     []string
		arrived = make(chan struct{}, 3)
		release = make(chan struct{})
		once    sync.Once
	)

	mi, err := activities.NewMultiInstance(
		activities.WithInputCollection("items", "item"),
		activities.WithOutputCollection("outs", "res"))
	require.NoError(t, err)

	items := data.MustProperty("items",
		data.MustItemDefinition(values.NewArray("a", "b", "c"),
			foundation.WithID("items")),
		data.ReadyDataState)

	outs := data.MustProperty("outs",
		data.MustItemDefinition(values.NewArray[any](),
			foundation.WithID("outs")),
		data.ReadyDataState)

	op, err := gooper.New("lm-par-op",
		func(ctx context.Context, r service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			item, err := r.GetData("item")
			if err != nil {
				return nil, err
			}

			arrived <- struct{}{}

			once.Do(func() {
				go func() {
					for i := 0; i < 3; i++ {
						<-arrived
					}
					close(release)
				}()
			})

			select {
			case <-release:
			case <-time.After(3 * time.Second):
				return nil, fmt.Errorf("passes did not overlap — " +
					"a sequential run, not a fan-out")
			}

			v := fmt.Sprint(item.Value().Get(ctx))

			mu.Lock()
			log = append(log, v)
			mu.Unlock()

			return data.MustItemDefinition(
				values.NewVariable("R:"+v),
				foundation.WithID("res")), nil
		})
	require.NoError(t, err)

	p, err := process.New("lm-par", foundation.WithID("lm-par"),
		data.WithProperties(items, outs))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	work, err := activities.NewServiceTask("work", op,
		activities.WithoutParams(), activities.WithLoop(mi),
		foundation.WithID("lm-par-work"))
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, work, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, work)
	link(t, work, end)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	inst := runLeafMI(t, s)

	mu.Lock()
	require.ElementsMatch(t, []string{"a", "b", "c"}, log)
	mu.Unlock()

	od, err := inst.sc.plane.GetData(scope.DataPath("/lm-par"), "outs")
	require.NoError(t, err)

	col, ok := od.Value().(data.Collection)
	require.True(t, ok)

	got := make([]any, 0, col.Count())
	for _, el := range col.GetAll(context.Background()) {
		got = append(got, el)
	}

	require.Equal(t, []any{"R:a", "R:b", "R:c"}, got,
		"outputs must land by ORDINAL, not completion order")
}

// TestLeafMIParallelCompletionCancels is T-4 (SRD-086 FR-2): the first
// drain's true completionCondition cancels the still-parked sibling
// leaf tracks; their slots keep nil and the run completes.
func TestLeafMIParallelCompletionCancels(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	cond := goexpr.Must(nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			d, err := ds.Find(ctx, "numberOfCompletedInstances")
			if err != nil {
				return nil, err
			}

			n, _ := d.Value().Get(ctx).(int)

			return values.NewVariable(n >= 1), nil
		})

	mi, err := activities.NewMultiInstance(
		activities.WithInputCollection("items", "item"),
		activities.WithOutputCollection("outs", "res"),
		activities.WithCompletionCondition(cond))
	require.NoError(t, err)

	items := data.MustProperty("items",
		data.MustItemDefinition(values.NewArray("a", "b", "c"),
			foundation.WithID("items")),
		data.ReadyDataState)

	outs := data.MustProperty("outs",
		data.MustItemDefinition(values.NewArray[any](),
			foundation.WithID("outs")),
		data.ReadyDataState)

	// the siblings must be PROVABLY blocked before "a" completes and
	// fires the condition — otherwise the test would pass even if they
	// had never started, and it would be asserting nothing about
	// cancellation.
	var blocked sync.WaitGroup

	blocked.Add(2)

	// ordinal 0 waits for both siblings to block, then returns; the
	// siblings stay parked until canceled.
	op, err := gooper.New("lm-can-op",
		func(ctx context.Context, r service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			item, err := r.GetData("item")
			if err != nil {
				return nil, err
			}

			v := fmt.Sprint(item.Value().Get(ctx))
			if v != "a" {
				blocked.Done()
				<-ctx.Done() // parked until the cancel

				return nil, ctx.Err()
			}

			blocked.Wait() // both siblings are in flight and blocked

			return data.MustItemDefinition(
				values.NewVariable("R:"+v),
				foundation.WithID("res")), nil
		})
	require.NoError(t, err)

	p, err := process.New("lm-can", foundation.WithID("lm-can"),
		data.WithProperties(items, outs))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	work, err := activities.NewServiceTask("work", op,
		activities.WithoutParams(), activities.WithLoop(mi),
		foundation.WithID("lm-can-work"))
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, work, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, work)
	link(t, work, end)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	inst := runLeafMI(t, s)

	od, err := inst.sc.plane.GetData(scope.DataPath("/lm-can"), "outs")
	require.NoError(t, err)

	col, ok := od.Value().(data.Collection)
	require.True(t, ok)

	all := col.GetAll(context.Background())
	require.Len(t, all, 3)
	require.Nil(t, all[1], "a canceled slot keeps its pre-run nil")
	require.Nil(t, all[2], "a canceled slot keeps its pre-run nil")
}

// gatedLeafMISnapshot builds the kill-and-resume leaf shape: pass "a"
// returns at once, passes "b"/"c" block until the gate opens — the
// crash window. count counts every op START (a re-run counts again:
// re-entering a step is at-least-once, SRD-070).
func gatedLeafMISnapshot(
	t *testing.T, key string, sequential bool,
	gate *atomic.Int32, count *atomic.Int32,
) *snapshot.Snapshot {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	opts := []activities.MultiInstanceOption{
		activities.WithInputCollection("items", "item"),
		activities.WithOutputCollection("outs", "res"),
	}
	if sequential {
		opts = append(opts, activities.WithSequential())
	}

	mi, err := activities.NewMultiInstance(opts...)
	require.NoError(t, err)

	items := data.MustProperty("items",
		data.MustItemDefinition(values.NewArray("a", "b", "c"),
			foundation.WithID("items")),
		data.ReadyDataState)

	outs := data.MustProperty("outs",
		data.MustItemDefinition(values.NewArray[any](),
			foundation.WithID("outs")),
		data.ReadyDataState)

	op, err := gooper.New(key+"-op",
		func(ctx context.Context, r service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			item, err := r.GetData("item")
			if err != nil {
				return nil, err
			}

			v := fmt.Sprint(item.Value().Get(ctx))

			count.Add(1)

			if v != "a" {
				for gate.Load() == 0 {
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-time.After(2 * time.Millisecond):
					}
				}
			}

			return data.MustItemDefinition(
				values.NewVariable("R:"+v),
				foundation.WithID("res")), nil
		})
	require.NoError(t, err)

	p, err := process.New(key, foundation.WithID(key),
		data.WithProperties(items, outs))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start",
		foundation.WithID(key+"-start"))
	require.NoError(t, err)

	work, err := activities.NewServiceTask("work", op,
		activities.WithoutParams(), activities.WithLoop(mi),
		foundation.WithID(key+"-work"))
	require.NoError(t, err)

	end, err := events.NewEndEvent("end", foundation.WithID(key+"-end"))
	require.NoError(t, err)

	for _, e := range []flow.Element{start, work, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, work)
	link(t, work, end)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	return s
}

// leafMIRec finds the recorded iteration position of the leaf node.
func leafMIRec(
	d *checkpoint.Document, nodeID string,
) *checkpoint.IterationRecord {
	for i := range d.Tracks {
		if d.Tracks[i].NodeID == nodeID {
			return d.Tracks[i].Iteration
		}
	}

	return nil
}

// liveOrdinals counts the instances an iteration record still shows as
// running — the ordinals a restore has to relaunch (SRD-090.A FR-7).
func liveOrdinals(rec *checkpoint.IterationRecord) int {
	live := 0

	for _, inst := range rec.Instances {
		if inst.State != "completed" {
			live++
		}
	}

	return live
}

// leafOuts reads the assembled output collection of a finished run.
func leafOuts(t *testing.T, inst *Instance, path string) []any {
	t.Helper()

	od, err := inst.sc.plane.GetData(scope.DataPath(path), "outs")
	require.NoError(t, err)

	col, ok := od.Value().(data.Collection)
	require.True(t, ok, "outs value is %T", od.Value())

	return col.GetAll(context.Background())
}

// TestLeafMISequentialKillAndResume is T-7 (SRD-086 FR-5), now reading
// the executor set TrackRecord.Iteration that replaced the MI mirror for
// a leaf (SRD-090.A FR-6): the position still rides the pass-posted
// mirror, and the restored run resumes at pass 2, never re-running the
// completed pass.
func TestLeafMISequentialKillAndResume(t *testing.T) {
	var gate, count atomic.Int32

	s := gatedLeafMISnapshot(t, "lm-skr", true, &gate, &count)

	doc := captureAt(t, s, func(d *checkpoint.Document) bool {
		rec := leafMIRec(d, "lm-skr-work")

		return rec != nil && rec.Completed == 1
	})

	// the checkpoint lands as pass a completes, so pass b may not have
	// entered its op yet — poll instead of asserting the instant.
	require.Eventually(t, func() bool { return count.Load() == 2 },
		3*time.Second, 5*time.Millisecond,
		"pass a completed, pass b started and blocked")

	gate.Store(1)

	restored := restoreToDone(t, doc, s)

	require.Equal(t, int32(4), count.Load(),
		"pass b re-runs (at-least-once), pass c runs, pass a NEVER "+
			"re-runs — the run is finished, so this count is final")

	outs := leafOuts(t, restored, "/lm-skr")
	require.Equal(t, []any{"R:a", "R:b", "R:c"}, normalized(outs),
		"the assembled output survives the crash in order")
}

// normalized unwraps any codec-wrapped elements so pre-kill (decoded)
// and post-kill (raw) slots compare uniformly.
func normalized(elems []any) []any {
	out := make([]any, 0, len(elems))

	for _, el := range elems {
		if v, ok := el.(data.Value); ok {
			el = v.Get(context.Background())
		}

		out = append(out, el)
	}

	return out
}

// TestLeafMIParallelKillAndResume is T-6 (SRD-086 FR-5), rewritten onto
// the executor set (SRD-090.A FR-6/FR-7). A parallel leaf no longer
// persists as a group record plus one track per open instance scope,
// because it no longer HAS instance scopes or per-instance tracks: it
// records which ordinals are still running, and restore relaunches
// exactly those. That a completed ordinal never re-runs is the property
// the old shape and this one both exist to protect.
func TestLeafMIParallelKillAndResume(t *testing.T) {
	var gate, count atomic.Int32

	s := gatedLeafMISnapshot(t, "lm-pkr", false, &gate, &count)

	doc := captureAt(t, s, func(d *checkpoint.Document) bool {
		rec := leafMIRec(d, "lm-pkr-work")

		return rec != nil && rec.Kind == "mi_parallel" && rec.Completed == 1 &&
			liveOrdinals(rec) == 2
	})

	require.Eventually(t, func() bool { return count.Load() == 3 },
		3*time.Second, 5*time.Millisecond,
		"all three started; a completed, b and c blocked in flight")

	gate.Store(1)

	restored := restoreToDone(t, doc, s)

	require.Equal(t, int32(5), count.Load(),
		"b and c re-run (at-least-once), a NEVER re-runs — the run is "+
			"finished, so this count is final")

	outs := leafOuts(t, restored, "/lm-pkr")
	require.Equal(t, []any{"R:a", "R:b", "R:c"}, normalized(outs),
		"outputs stay positional across the crash")
}

// TestLeafMISequentialMissingOutput: an MI declaring an output
// collection whose pass produces no output item is a loud fault —
// captureLeafOutput's lookup fails (SRD-086 FR-1).
func TestLeafMISequentialMissingOutput(t *testing.T) {
	var (
		mu  sync.Mutex
		log []string
	)

	_ = log

	s := leafMISnapshot(t, "lm-noout2", &mu, &log)

	// the harness op returns "res"; rebuild with an op that doesn't.
	require.NoError(t, data.CreateDefaultStates())

	mi, err := activities.NewMultiInstance(
		activities.WithSequential(),
		activities.WithInputCollection("items", "item"),
		activities.WithOutputCollection("outs", "res"))
	require.NoError(t, err)

	items := data.MustProperty("items",
		data.MustItemDefinition(values.NewArray("x"),
			foundation.WithID("items")),
		data.ReadyDataState)

	outs := data.MustProperty("outs",
		data.MustItemDefinition(values.NewArray[any](),
			foundation.WithID("outs")),
		data.ReadyDataState)

	op, err := gooper.New("lm-noout2-op",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			return nil, nil // declared output never produced
		})
	require.NoError(t, err)

	p, err := process.New("lm-noout2b", foundation.WithID("lm-noout2b"),
		data.WithProperties(items, outs))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	work, err := activities.NewServiceTask("work", op,
		activities.WithoutParams(), activities.WithLoop(mi))
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, work, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, work)
	link(t, work, end)

	s, err = snapshot.New(p)
	require.NoError(t, err)

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(),
		&recordingProducer{}, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, inst.Run(ctx))

	select {
	case <-inst.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("did not finish")
	}

	require.NotEqual(t, Completed, inst.State(),
		"a declared-but-missing output item faults the pass loud")
}
