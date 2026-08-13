package instance

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
)

// capturedRequest runs e against a stand-in loop that records the scopeRequest
// it receives, replies, and immediately reports the scope drained. It returns
// the request the executor sent — the thing under test, since an executor's
// whole contribution to a fanned-out open is what it puts on that request.
func capturedRequest(
	t *testing.T, inst *Instance, host *track, e *scopeExec,
) scopeRequest {
	t.Helper()

	got := make(chan scopeRequest, 1)

	go func() {
		req := <-inst.scopeReq
		got <- req
		req.reply <- scopeReply{scopePath: host.scopePath}
		close(e.drain) // the loop reports this instance's scope drained
	}()

	_, err := e.run(t.Context())
	require.NoError(t, err)

	return <-got
}

// TestCompositeInstanceCarriesItsOwnScope pins what separates a fanned-out
// composite instance from a sequential pass (SRD-090.A M3b): its own scope
// segment, and the per-instance data published there.
//
// The segment comes from the EXECUTOR rather than being derived loop-side,
// because deriving it in handleScopeOpen would have moved the sequential
// path's data paths, observability facts and restore compatibility — none of
// which this milestone is entitled to change.
func TestCompositeInstanceCarriesItsOwnScope(t *testing.T) {
	inst, _, node, host := decoratorFixture(t)

	d := newIterDecorator(host, &stepInfo{node: node}, nil, true)
	host.miState = &miState{}

	e, err := d.compositeInstanceFor(t.Context(), 2)
	require.NoError(t, err)

	se, ok := e.(*scopeExec)
	require.True(t, ok, "a composite instance is a scope executor")

	req := capturedRequest(t, inst, host, se)

	require.Equal(t, scopeSegment(node)+"-2", req.segment,
		"instance 2 opens sp-<id>-2, not the node's shared sp-<id>")
	require.Equal(t, 2, req.ordinal,
		"and reports its OWN ordinal as its Opened fact (SRD-056.A FR-14)")
	require.Equal(t, []miBinding{{name: "loopCounter", value: 2}}, req.binds,
		"a cardinality-driven instance publishes only its loopCounter")
	require.Nil(t, req.capture,
		"an activity assembling no output allocates no capture cell")
}

// TestCompositeInstanceSplitsItsInputItem: a collection-driven iteration
// hands instance ord the ord-th element, bound at that instance's OWN scope.
//
// The scope is the point. The sequential slice binds the same names at the
// shared HOST scope, which N concurrent siblings would overwrite; a per-
// instance scope is what makes the same data concurrency-safe.
func TestCompositeInstanceSplitsItsInputItem(t *testing.T) {
	_, _, node, host := decoratorFixture(t)

	d := newIterDecorator(host, &stepInfo{node: node}, nil, true)
	host.miState = &miState{
		collection: values.NewArray[any]("a", "b", "c"),
		inputItem:  "item",
	}

	e, err := d.compositeInstanceFor(t.Context(), 1)
	require.NoError(t, err)

	require.Equal(t,
		[]miBinding{
			{name: "loopCounter", value: 1},
			{name: "item", value: any("b")},
		},
		e.(*scopeExec).binds)
}

// TestCompositeInstanceRefusesABrokenCollection: the per-instance split fails
// fast when the input collection's read errors, before any scope is opened.
func TestCompositeInstanceRefusesABrokenCollection(t *testing.T) {
	_, _, node, host := decoratorFixture(t)

	d := newIterDecorator(host, &stepInfo{node: node}, nil, true)
	host.miState = &miState{
		collection: getAtErrColl{values.NewArray[any](1, 2, 3)},
		inputItem:  "item",
	}

	_, err := d.compositeInstanceFor(t.Context(), 0)
	require.Error(t, err)
}

// TestCompositeInstanceAllocatesItsCapture: an output-assembling activity
// gives each instance a cell naming the item to read from its child scope.
func TestCompositeInstanceAllocatesItsCapture(t *testing.T) {
	_, _, node, host := decoratorFixture(t)

	d := newIterDecorator(host, &stepInfo{node: node}, nil, true)
	host.miState = &miState{
		staging:    values.NewArray[any](),
		outputItem: "result",
	}

	e, err := d.compositeInstanceFor(t.Context(), 0)
	require.NoError(t, err)

	require.Equal(t, &instanceCapture{item: "result"}, e.(*scopeExec).capture)
}

// TestCaptureInstanceOutputReadsTheDrainingScope pins the loop-side half of
// the handoff: a composite's output lives in a child scope that is about to
// close, so it is read HERE, before completeScope closes it — the last point
// the data exists.
func TestCaptureInstanceOutputReadsTheDrainingScope(t *testing.T) {
	inst, ls, node, host := decoratorFixture(t)
	require.NoError(t,
		inst.sc.bindDataItemAt(host.scopePath, "result", 42))

	c := &instanceCapture{item: "result"}
	entry := &scopeEntry{host: host, node: node, capture: c}

	require.NoError(t,
		ls.captureInstanceOutput(t.Context(), entry, host.scopePath))
	require.True(t, c.filled)
	require.Equal(t, 42, c.value)
}

// TestCaptureInstanceOutputToleratesNothingToCapture: neither an instance
// with no cell nor one whose declared output was never produced is an error.
// An optional output is ordinary, and the unfilled slot keeps its nil exactly
// as a canceled instance's does (SRD-056.A §2.7).
func TestCaptureInstanceOutputToleratesNothingToCapture(t *testing.T) {
	_, ls, node, host := decoratorFixture(t)

	for name, entry := range map[string]*scopeEntry{
		"no cell at all": {host: host, node: node},
		"cell names no item": {
			host: host, node: node, capture: &instanceCapture{}},
		"item was never produced": {
			host: host, node: node,
			capture: &instanceCapture{item: "absent"}},
	} {
		t.Run(name, func(t *testing.T) {
			require.NoError(t,
				ls.captureInstanceOutput(t.Context(), entry, host.scopePath))

			if entry.capture != nil {
				require.False(t, entry.capture.filled)
			}
		})
	}
}

// TestCollectOutputStagesAFilledCell pins the handoff the drain close makes
// safe: the loop fills a composite instance's cell before closing its scope,
// and the barrier moves it into the positional slot once that instance
// reports (SRD-090.A M3b).
//
// A leaf fan-out has no cells at all, and an instance that produced nothing
// leaves its slot nil exactly as a canceled one does (SRD-056.A §2.7) — so
// all three cases have to read as themselves, not as zero values.
func TestCollectOutputStagesAFilledCell(t *testing.T) {
	run := parallelRun{
		outs: newInstanceOutputs(3),
		caps: map[int]*instanceCapture{
			0: {item: "result", value: "done", filled: true},
			1: {item: "result"}, // opened, produced no output
			// 2 is a leaf instance: no cell at all
		},
	}

	for ord := range 3 {
		run.collectOutput(ord)
	}

	require.Equal(t, []any{"done", nil, nil}, run.outs.values)
	require.Equal(t, []bool{true, false, false}, run.outs.filled,
		"only a FILLED cell claims its slot — the other two stay open for "+
			"a leaf's own frame capture, or stay nil like a canceled one")
}
