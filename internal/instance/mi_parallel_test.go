package instance

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/generated/mockdata"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	dgexpr "github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// attrAtLeast builds the boolean completionCondition "<attr> >= n" over a
// §2.9 runtime attribute published at the host scope.
func attrAtLeast(t *testing.T, attr string, n int) data.FormalExpression {
	t.Helper()

	return goexpr.Must(nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			d, err := ds.Find(ctx, attr)
			if err != nil {
				return nil, err
			}

			v, _ := d.Value().Get(ctx).(int)

			return values.NewVariable(v >= n), nil
		})
}

// mustParallelMI builds a valid PARALLEL Multi-Instance — NewMultiInstance
// without WithSequential (parallel is the §13.3.7 default).
func mustParallelMI(
	t *testing.T, opts ...activities.MultiInstanceOption,
) *activities.MultiInstanceLoopCharacteristics {
	t.Helper()

	mi, err := activities.NewMultiInstance(opts...)
	require.NoError(t, err)

	return mi
}

// TestParallelMultiInstanceRunsAll: a parallel Multi-Instance fans out all N
// instances and completes when the last drains (SRD-056.A FR-1/FR-2/FR-3).
func TestParallelMultiInstanceRunsAll(t *testing.T) {
	var count atomic.Int32

	mi := mustParallelMI(t, activities.WithCardinality(cardExpr(t, 3)))

	inst := miSubProcessInstance(t, &count, mi)
	runToDone(t, inst)

	require.Equal(t, Completed, inst.State())
	require.Equal(t, int32(3), count.Load(),
		"all N instances run and the activity completes")
}

// TestParallelMultiInstanceZeroCardinality: a zero count runs no instances, yet
// the host resumes and the activity completes.
func TestParallelMultiInstanceZeroCardinality(t *testing.T) {
	var count atomic.Int32

	mi := mustParallelMI(t, activities.WithCardinality(cardExpr(t, 0)))

	inst := miSubProcessInstance(t, &count, mi)
	runToDone(t, inst)

	require.Equal(t, Completed, inst.State())
	require.Equal(t, int32(0), count.Load())
}

// TestParallelMultiInstanceDistinctScopes: each instance opens a distinct scope
// carrying its own 0-based ordinal (FR-2/FR-11).
func TestParallelMultiInstanceDistinctScopes(t *testing.T) {
	var count atomic.Int32

	mi := mustParallelMI(t, activities.WithCardinality(cardExpr(t, 3)))

	inst := miSubProcessInstance(t, &count, mi)

	rec := &obsRecorder{}
	inst.AddObserver(rec.record)

	runToDone(t, inst)

	seen := map[string]bool{}
	rec.mu.Lock()
	for _, e := range rec.events {
		if e.Kind == observability.KindScope &&
			e.Phase == observability.PhaseOpened {
			if lc, ok := e.Details[observability.AttrLoopCounter]; ok {
				seen[lc] = true
			}
		}
	}
	rec.mu.Unlock()

	require.True(t, seen["0"] && seen["1"] && seen["2"],
		"each parallel instance opens a distinct scope with its ordinal")
}

// TestParallelMultiInstanceInputItemPerScope: each concurrent instance sees its
// own collection element bound as `item` in its own scope (FR-5). Parallel
// completion order is nondeterministic, so the SET is asserted.
func TestParallelMultiInstanceInputItemPerScope(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var (
		mu   sync.Mutex
		seen []any
	)

	op, err := gooper.New("read-item",
		func(ctx context.Context, r service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			d, err := r.GetData("item")
			if err != nil {
				return nil, err
			}

			mu.Lock()
			seen = append(seen, d.Value().Get(ctx))
			mu.Unlock()

			return nil, nil
		})
	require.NoError(t, err)

	mi := mustParallelMI(t, activities.WithInputCollection("items", "item"))
	items := data.MustProperty("items",
		data.MustItemDefinition(values.NewArray(10, 20, 30),
			foundation.WithID("items")),
		data.ReadyDataState)

	inst := miSubProcessInstanceOp(t, op, mi, items)
	runToDone(t, inst)

	require.Equal(t, Completed, inst.State())

	mu.Lock()
	got := append([]any{}, seen...)
	mu.Unlock()

	require.ElementsMatch(t, []any{10, 20, 30}, got,
		"each instance sees its own element (order nondeterministic)")
}

// TestParallelMultiInstanceAssemblesOutput: per-instance outputs assemble
// positionally (slot = ordinal) into the output collection, published once at
// completion — in input order despite concurrent, out-of-order completion
// (FR-6/FR-7).
func TestParallelMultiInstanceAssemblesOutput(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	op, err := gooper.New("double",
		func(ctx context.Context, r service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			d, err := r.GetData("item")
			if err != nil {
				return nil, err
			}

			v, _ := d.Value().Get(ctx).(int)

			return data.MustItemDefinition(
				values.NewVariable(v*2), foundation.WithID("out")), nil
		})
	require.NoError(t, err)

	mi := mustParallelMI(t,
		activities.WithInputCollection("nums", "item"),
		activities.WithOutputCollection("doubled", "out"))
	nums := data.MustProperty("nums",
		data.MustItemDefinition(values.NewArray(2, 3, 4),
			foundation.WithID("nums")),
		data.ReadyDataState)

	inst := miSubProcessInstanceOp(t, op, mi, nums)
	runToDone(t, inst)

	require.Equal(t, Completed, inst.State())

	d, err := inst.DataReader().GetData("doubled")
	require.NoError(t, err)
	col, ok := d.Value().(data.Collection)
	require.True(t, ok, "loopDataOutputRef is a collection")
	require.Equal(t, []any{4, 6, 8}, col.GetAll(context.Background()),
		"positional assembly, input order, despite concurrent completion")
}

// TestParallelMultiInstanceOutputItemMissing: an output-collecting parallel MI
// whose body produces no output item faults when the item is captured.
func TestParallelMultiInstanceOutputItemMissing(t *testing.T) {
	var count atomic.Int32

	mi := mustParallelMI(t,
		activities.WithCardinality(cardExpr(t, 3)),
		activities.WithOutputCollection("coll", "missing"))

	inst := miSubProcessInstance(t, &count, mi)
	runToDone(t, inst)

	require.NotEqual(t, Completed, inst.State(),
		"a missing outputDataItem faults the instance")
}

// TestParallelMultiInstanceCompletionCancelsRemainder (FR-8): once the
// completionCondition holds, the still-running instances are canceled as a
// unit. Bodies block per-instance so the truncation is deterministic (parallel
// instant bodies would all complete first).
func TestParallelMultiInstanceCompletionCancelsRemainder(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	const total = 5

	gates := make([]chan struct{}, total)
	for i := range gates {
		gates[i] = make(chan struct{})
	}

	var canceled atomic.Int32

	// Every body reports that it is RUNNING before it blocks. Without this, an
	// instance canceled before its body was ever scheduled never reaches the
	// select and so never counts itself — the assertion below then waits for a
	// third cancellation that can never arrive. Releasing the two winners only
	// after all five are parked is what makes the truncation deterministic, which
	// is what this test claims to rely on.
	entered := make(chan struct{}, total)

	op, err := gooper.New("wait",
		func(ctx context.Context, r service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			d, err := r.GetData("loopCounter")
			if err != nil {
				return nil, err
			}

			i, _ := d.Value().Get(ctx).(int)

			entered <- struct{}{}

			select {
			case <-gates[i]: // released → completes normally
			case <-ctx.Done(): // canceled by the completionCondition
				canceled.Add(1)
			}

			return nil, nil
		})
	require.NoError(t, err)

	mi := mustParallelMI(t,
		activities.WithCardinality(cardExpr(t, total)),
		activities.WithCompletionCondition(
			attrAtLeast(t, "numberOfCompletedInstances", 2)))

	// release exactly two instances; the other three must be canceled — they
	// never see their gate, so a completing run proves cancellation. The release
	// waits until all five bodies are parked, so no instance can be canceled
	// before it has started.
	go func() {
		for range total {
			<-entered
		}

		close(gates[0])
		close(gates[1])
	}()

	inst := miSubProcessInstanceOp(t, op, mi)
	runToDone(t, inst)

	require.Equal(t, Completed, inst.State())
	require.Eventually(t, func() bool { return canceled.Load() == total-2 },
		2*time.Second, 5*time.Millisecond,
		"the three not-yet-completed instances are canceled")
}

// TestParallelMultiInstanceRuntimeAttributes (FR-9): the §2.9 attributes are
// published at the host scope and readable by the completionCondition — a
// never-true condition over numberOfInstances runs every instance (and a
// missing attribute would fault the evaluation).
func TestParallelMultiInstanceRuntimeAttributes(t *testing.T) {
	var count atomic.Int32

	mi := mustParallelMI(t,
		activities.WithCardinality(cardExpr(t, 3)),
		activities.WithCompletionCondition(
			attrAtLeast(t, "numberOfInstances", 100)))

	inst := miSubProcessInstance(t, &count, mi)
	runToDone(t, inst)

	require.Equal(t, Completed, inst.State())
	require.Equal(t, int32(3), count.Load(),
		"the never-true condition lets all instances run")
}

// TestParallelMultiInstanceBoundaryInterruptsAll (FR-10): an interrupting
// boundary firing on a fanned-out host tears down ALL N instance scopes, not
// just the default `sp-<id>` segment a serial host would hold.
//
// It reads the entries themselves (instanceScopesOf), which is what makes
// this the same teardown a fired completionCondition asks for; the two were
// separate mechanisms over one question while the group existed.
func TestParallelMultiInstanceBoundaryInterruptsAll(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	mi := mustParallelMI(t, activities.WithCardinality(cardExpr(t, 3)))
	inst := miSubProcessInstance(t, new(atomic.Int32), mi)
	inst.tracks = map[string]*track{}
	ls := newLoopState(inst)
	node := findNode(t, inst.s, "body")

	host, err := newTrack(node, inst, nil)
	require.NoError(t, err)

	// three in-flight instances, as a fan-out leaves them.
	for i := range 3 {
		child, err := host.scopePath.Append(
			scopeSegment(node) + "-" + strconv.Itoa(i))
		require.NoError(t, err)
		require.NoError(t, inst.sc.plane.OpenScope(child))

		ls.scopes[child] = &scopeEntry{
			host: host, node: node, ordinal: i, instance: true,
		}
	}

	ls.cancelHostScope(host)

	require.Empty(t, ls.scopes, "all N parallel instance scopes are canceled")
}

// TestParallelMultiInstanceNonBoolCompletion: a completionCondition that
// evaluates to a non-boolean value faults the instance.
func TestParallelMultiInstanceNonBoolCompletion(t *testing.T) {
	var count atomic.Int32

	me := mockdata.NewMockFormalExpression(t)
	me.EXPECT().Language().Return(dgexpr.Language).Maybe()
	me.EXPECT().ResultType().Return("bool")
	me.EXPECT().Language().Return("mock").Maybe()
	me.EXPECT().Evaluate(mock.Anything, mock.Anything).
		Return(values.NewVariable(42), nil).Maybe()

	mi := mustParallelMI(t,
		activities.WithCardinality(cardExpr(t, 3)),
		activities.WithCompletionCondition(me))

	inst := miSubProcessInstance(t, &count, mi)
	runToDone(t, inst)

	require.NotEqual(t, Completed, inst.State(),
		"a non-boolean completionCondition faults the instance")
}

// TestParallelMultiInstancePublishError covers the completion publish's
// error: committing the assembled collection at an unopened host scope fails
// when the decorator publishes it on the way out.
func TestParallelMultiInstancePublishError(t *testing.T) {
	var count atomic.Int32

	mi := mustParallelMI(t, activities.WithCardinality(cardExpr(t, 1)),
		activities.WithOutputCollection("res", "out"))

	inst := miSubProcessInstance(t, &count, mi)
	inst.tracks = map[string]*track{}
	node := findNode(t, inst.s, "body")

	host, err := newTrack(node, inst, nil)
	require.NoError(t, err)

	// an unopened host scope makes the publish Commit fail.
	host.scopePath = scope.DataPath("/does/not/exist")
	host.miState = &miState{
		staging: values.NewArray[any](nil), outputRef: "res",
	}

	require.Error(t, miIterator{mi: mi}.publishOutput(host))
}

// TestParallelMultiInstanceCardinalityError: a cardinality that errors on
// evaluation faults the instance before any instance opens.
func TestParallelMultiInstanceCardinalityError(t *testing.T) {
	var count atomic.Int32

	mi := mustParallelMI(t, activities.WithCardinality(cardExprBoom(t)))

	inst := miSubProcessInstance(t, &count, mi)
	runToDone(t, inst)

	require.NotEqual(t, Completed, inst.State(),
		"a cardinality evaluation error faults the instance")
	require.Equal(t, int32(0), count.Load())
}

// TestParallelMultiInstanceOpenScopeError covers an instance open's
// defensive data-plane failure: a child path already open in the PLANE but
// carrying no entry cannot be re-attached to (there is nothing to re-attach
// to) and cannot be opened either, so the instance faults.
func TestParallelMultiInstanceOpenScopeError(t *testing.T) {
	var count atomic.Int32

	mi := mustParallelMI(t, activities.WithCardinality(cardExpr(t, 2)))

	inst := miSubProcessInstance(t, &count, mi)
	inst.tracks = map[string]*track{}
	ls := newLoopState(inst)
	node := findNode(t, inst.s, "body")

	host, err := newTrack(node, inst, nil)
	require.NoError(t, err)

	seg := scopeSegment(node) + "-0"

	child, err := host.scopePath.Append(seg)
	require.NoError(t, err)
	require.NoError(t, inst.sc.plane.OpenScope(child))

	reply := make(chan scopeReply, 1)
	ls.handleScopeOpen(t.Context(), scopeRequest{
		op: scopeOpen, host: host, node: node, segment: seg,
		drain: make(chan struct{}), reply: reply,
	})
	require.Error(t, (<-reply).err)
}

// TestParallelMultiInstanceSequentialStillWorks: the parallel dispatch leaves the
// serial Multi-Instance path (SRD-055) untouched (NFR-2).
func TestParallelMultiInstanceSequentialStillWorks(t *testing.T) {
	var count atomic.Int32

	mi := mustSeqMI(t, activities.WithCardinality(cardExpr(t, 3)))

	inst := miSubProcessInstance(t, &count, mi)
	runToDone(t, inst)

	require.Equal(t, Completed, inst.State())
	require.Equal(t, int32(3), count.Load())
}

// TestParallelStepBindError: the per-completion §2.9 attribute bind at an
// unopened host scope faults the barrier step (covering bindMICounters' error
// path too).
func TestParallelStepBindError(t *testing.T) {
	_, node, host := miParFixture(t)
	host.scopePath = scope.DataPath("/does/not/exist") // bindDataItemAt fails

	mi := multiInstanceOf(node)
	d := newIterDecorator(host, &stepInfo{node: node}, mi, true)

	_, err := d.parallelStep(t.Context(), miIterator{mi: mi}, 3, 1, 0)
	require.Error(t, err)
}

// TestRunMIParallelDrainError is the parallel twin of
// TestRunMISequentialDrainError: a stand-in loop takes the fan-out's scope
// opens and then stops, so every instance's drain wait unblocks with an error
// rather than hanging on scopes that will never close.
//
// The barrier must still take ALL launched reports and fault the run once,
// rather than returning on the first — an abandoned barrier leaves the
// still-running instances' scopes open, which is what M4c's `fail` exists to
// prevent.
func TestRunMIParallelDrainError(t *testing.T) {
	inst, node, host := miParFixture(t)

	go func() {
		// serve the fan-out's opens, then stop the loop without ever
		// draining any of them.
		for range 3 {
			req, ok := <-inst.scopeReq
			if !ok {
				return
			}

			req.reply <- scopeReply{scopePath: host.scopePath}
		}

		close(inst.loopDone)
	}()

	_, err := newIterDecorator(
		host, &stepInfo{node: node}, multiInstanceOf(node), true,
	).run(t.Context())

	require.Error(t, err, "the run faults rather than hanging")
}

// TestParallelBarrierKeepsATeardownError (SRD-090.A M4c): when a mid-barrier
// failure's teardown ALSO fails, the teardown's error is what the run
// reports — it is the more serious of the two, since it means instance scopes
// were left open.
//
// Driven through the barrier rather than by calling stopRemaining directly.
// The isolated call proves the helper returns an error; it cannot prove the
// caller keeps it, which is the half that would silently regress if `fail`
// dropped the assignment.
func TestParallelBarrierKeepsATeardownError(t *testing.T) {
	inst, node, host := miParFixture(t)
	close(inst.loopDone) // the teardown roundtrip returns not-running

	d := newIterDecorator(host, &stepInfo{node: node},
		multiInstanceOf(node), true)

	b := &parallelBarrier{
		d:   d,
		run: parallelRun{cancelRest: func() {}},
	}

	b.fail(t.Context(), errors.New("the instance failed"))

	require.Error(t, b.err)
	require.NotContains(t, b.err.Error(), "the instance failed",
		"the teardown failure replaces the instance's — scopes stayed open")
	require.True(t, b.stopping)
}

// TestRunMIParallelBindError is the parallel twin of
// TestRunMISequentialBindError: the per-instance input split fails when the
// input collection's GetAt errors, and the fan-out faults before any scope
// opens.
//
// It asserts the fault arrives WITHIN a bound rather than merely arriving.
// The unit-level guard test proves compositeInstanceFor returns an error; it
// cannot prove the fan-out that calls it does not then sit waiting on
// instances it never launched, which is the half a barrier can get wrong.
func TestRunMIParallelBindError(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var count atomic.Int32

	mi := mustParallelMI(t, activities.WithInputCollection("items", "item"))
	inst := miSubProcessInstance(t, &count, mi)
	inst.tracks = map[string]*track{}
	node := findNode(t, inst.s, "body")

	host, err := newTrack(node, inst, nil)
	require.NoError(t, err)

	coll := getAtErrColl{values.NewArray[any](1, 2, 3)}
	require.NoError(t, inst.sc.bindValueAt(host.scopePath, "items", coll))

	errCh := make(chan error, 1)

	go func() {
		_, rerr := newIterDecorator(
			host, &stepInfo{node: node}, multiInstanceOf(node), true,
		).run(t.Context())

		errCh <- rerr
	}()

	select {
	case rerr := <-errCh:
		require.Error(t, rerr)

	case <-time.After(5 * time.Second):
		t.Fatal("the fan-out never returned — it is waiting on instances " +
			"the failed split never launched")
	}
}
