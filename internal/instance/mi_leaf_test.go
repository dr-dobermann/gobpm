package instance

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/enginert"
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
