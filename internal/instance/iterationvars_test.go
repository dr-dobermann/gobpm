package instance

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
)

// iterationVarsSeen runs a three-iteration iterated Service Task and returns
// what each pass read through its OWN data source — the surface a model reads
// through, rather than the binder that wrote the values.
func iterationVarsSeen(
	t *testing.T, key string, miOpts ...activities.MultiInstanceOption,
) []map[string]string {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	var (
		mu   sync.Mutex
		seen []map[string]string
	)

	op, err := gooper.New(key+"-op",
		func(ctx context.Context, r service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			got := map[string]string{}

			for _, n := range []string{
				"loopCounter", IterationNumber, IterationID, IterationMode,
			} {
				d, gerr := r.GetData(n)
				if gerr != nil {
					return nil, fmt.Errorf("read %q: %w", n, gerr)
				}

				got[n] = fmt.Sprint(d.Value().Get(ctx))
			}

			mu.Lock()
			seen = append(seen, got)
			mu.Unlock()

			return data.MustItemDefinition(
				values.NewVariable("ok"), foundation.WithID("res")), nil
		})
	require.NoError(t, err)

	opts := append([]activities.MultiInstanceOption{
		activities.WithInputCollection("items", "item"),
	}, miOpts...)

	mi, err := activities.NewMultiInstance(opts...)
	require.NoError(t, err)

	items := data.MustProperty("items",
		data.MustItemDefinition(values.NewArray("a", "b", "c"),
			foundation.WithID("items")),
		data.ReadyDataState)

	p, err := process.New(key, foundation.WithID(key),
		data.WithProperties(items))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start", foundation.WithID(key+"-start"))
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

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(),
		&recordingProducer{}, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, inst.Run(ctx))

	<-inst.Done()

	mu.Lock()
	defer mu.Unlock()

	return append([]map[string]string(nil), seen...)
}

// TestIterationVarsAreTheExecutionsOwn (SRD-090.D T-5, T-7): every iteration of
// an iterated activity reads ITERATION_NUMBER, ITERATION_ID and ITERATION_MODE
// as ITS OWN, on both publication paths.
//
// The three are values of the EXECUTION: N instances can read them at the same
// moment and each must get its own answer. That is exactly the property that
// keeps them out of a flat RUNTIME name, where a supplier handed only a name
// could not say whose ordinal was asked for (ADR-025 §2.9.2) — so it is worth
// pinning at the surface a model actually reads through, not at the binder.
func TestIterationVarsAreTheExecutionsOwn(t *testing.T) {
	for name, tc := range map[string]struct {
		opts []activities.MultiInstanceOption
		mode string
	}{
		"sequential Multi-Instance binds at the activity's own scope": {
			opts: []activities.MultiInstanceOption{activities.WithSequential()},
			mode: iterKindMISequential,
		},
		"parallel Multi-Instance binds frame-local": {
			opts: nil,
			mode: iterKindMIParallel,
		},
	} {
		t.Run(name, func(t *testing.T) {
			seen := iterationVarsSeen(t, "iv-"+tc.mode, tc.opts...)
			require.Len(t, seen, 3, "one reading per instance")

			ordinals := map[string]bool{}
			ids := map[string]bool{}

			for _, got := range seen {
				require.Equal(t, got["loopCounter"], got[IterationNumber],
					"ITERATION_NUMBER is the ordinal loopCounter already "+
						"reports, under the engine's own name — one value "+
						"with two spellings cannot disagree, two values can")

				require.Equal(t, tc.mode, got[IterationMode],
					"ITERATION_MODE is the record's own kind, so the model "+
						"and the runtime say the same word for the shape")

				require.Contains(t, got[IterationID], got[IterationNumber],
					"the identity carries the ordinal it is derived from")

				ordinals[got[IterationNumber]] = true
				ids[got[IterationID]] = true
			}

			require.Len(t, ordinals, 3,
				"each instance read ITS OWN ordinal, not a sibling's")
			require.Len(t, ids, 3,
				"and its own identity — the property a flat RUNTIME name "+
					"could not have provided")
		})
	}
}

// TestIterationIDIsDerivedNotMinted (SRD-090.D T-6, ADR-025 §2.9.3): an
// iteration's identity is assembled from state that already survives a
// checkpoint — the enclosing scope path, the activity id, the ordinal — so it
// is stable with nothing stored for it.
//
// Asserted over the deriver, because the property IS that the same inputs
// always yield the same identity. A minted id would have to be persisted to
// have it, adding a field whose only job is to say what the other three
// already say.
func TestIterationIDIsDerivedNotMinted(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	node, err := events.NewEndEvent("approve", foundation.WithID("approve"))
	require.NoError(t, err)

	first := iterationIDOf("/p/sub", node, 2)

	require.Equal(t, first, iterationIDOf("/p/sub", node, 2),
		"the same instance derives the same identity every time — which is "+
			"what lets it survive a restore with nothing recorded for it")

	require.NotEqual(t, first, iterationIDOf("/p/sub", node, 3),
		"a sibling ordinal is a different instance")
	require.NotEqual(t, first, iterationIDOf("/p/other", node, 2),
		"the same ordinal of the same activity in another scope is a "+
			"different instance — which is why the scope path is in the "+
			"identity at all")

	require.Contains(t, first, node.ID(), "the activity is named")
	require.Contains(t, first, "/p/sub", "and where it runs")
}

// TestIterationsOutlivesTheActivity (SRD-090.D T-8, ADR-025 §2.9.2): a node
// AFTER an iterated activity can still ask what that activity did.
//
// This is the question the counts cannot answer at any address. They are bound
// at the activity's own scope and end with the activation, which is correct for
// them — "how many are running" means nothing once nothing is. The register is
// keyed by activity id and served from the instance, so it survives the
// activity and stays unambiguous when two activities iterate at once.
func TestIterationsOutlivesTheActivity(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var (
		mu    sync.Mutex
		after string
	)

	// the reader runs on the node FOLLOWING the Multi-Instance, by which time
	// the activity's own scope publishes nothing.
	readAfter, err := gooper.New("io-after-op",
		func(ctx context.Context, r service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			d, gerr := r.GetData("RUNTIME/" + Iterations)
			if gerr != nil {
				return nil, fmt.Errorf("read the register: %w", gerr)
			}

			mu.Lock()
			after = fmt.Sprint(d.Value().Get(ctx))
			mu.Unlock()

			return data.MustItemDefinition(
				values.NewVariable("ok"), foundation.WithID("res")), nil
		})
	require.NoError(t, err)

	work, err := gooper.New("io-work-op",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			return data.MustItemDefinition(
				values.NewVariable("ok"), foundation.WithID("res")), nil
		})
	require.NoError(t, err)

	mi, err := activities.NewMultiInstance(
		activities.WithInputCollection("items", "item"))
	require.NoError(t, err)

	items := data.MustProperty("items",
		data.MustItemDefinition(values.NewArray("a", "b", "c"),
			foundation.WithID("items")),
		data.ReadyDataState)

	p, err := process.New("iter-outlives",
		foundation.WithID("iter-outlives"), data.WithProperties(items))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start", foundation.WithID("io-start"))
	require.NoError(t, err)

	fan, err := activities.NewServiceTask("fan", work,
		activities.WithoutParams(), activities.WithLoop(mi),
		foundation.WithID("io-fan"))
	require.NoError(t, err)

	next, err := activities.NewServiceTask("next", readAfter,
		activities.WithoutParams(), foundation.WithID("io-next"))
	require.NoError(t, err)

	end, err := events.NewEndEvent("end", foundation.WithID("io-end"))
	require.NoError(t, err)

	for _, e := range []flow.Element{start, fan, next, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, fan)
	link(t, fan, next)
	link(t, next, end)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(),
		&recordingProducer{}, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, inst.Run(ctx))

	<-inst.Done()

	mu.Lock()
	defer mu.Unlock()

	require.Contains(t, after, "io-fan",
		"the register is keyed by ACTIVITY ID — what keeps it unambiguous "+
			"when two activities iterate at once, and what a flat runtime "+
			"name could not have done")
	require.Contains(t, after, iterKindMIParallel, "it records the shape")
	require.Contains(t, after, "3", "and the total it fanned out to")
}

// TestIterationVarsPublishNothingForANonIteration (SRD-090.D FR-3): the
// builders are shared by paths that are not always iterating, so "this
// activity does not iterate" has to be expressible as publishing nothing.
//
// A caller with no iteration kind gets no values rather than a set of empty
// ones: an activity that runs once must not grow an ITERATION_MODE of "" that
// a model could read and branch on.
func TestIterationVarsPublishNothingForANonIteration(t *testing.T) {
	vars, err := iterationVars("/p", "", nil, 0)
	require.NoError(t, err)
	require.Nil(t, vars, "no kind, no publication")

	require.Nil(t, iterationBindings("/p", "", nil, 0),
		"and the host-scope form agrees — one rule, both paths")

	// with a kind, both forms publish the same three names, so the two
	// publication paths cannot drift in WHAT they expose.
	vars, err = iterationVars("/p", iterKindStdLoop, nil, 1)
	require.NoError(t, err)
	require.Len(t, vars, 3)

	binds := iterationBindings("/p", iterKindStdLoop, nil, 1)
	require.Len(t, binds, 3)

	names := map[string]bool{}
	for _, v := range vars {
		names[v.Name()] = true
	}

	for _, b := range binds {
		require.True(t, names[b.name],
			"%q is published by one path and not the other", b.name)
	}
}

// TestIterationsRegisterIgnoresAnUnnamedActivity (SRD-090.D FR-4): the register
// is keyed by activity id, so an entry with no key would be unreachable — and
// worse, indistinguishable from another unnamed one. It is refused rather than
// stored under "".
func TestIterationsRegisterIgnoresAnUnnamedActivity(t *testing.T) {
	reg := newIterations()

	require.Nil(t, reg.snapshot(), "nothing recorded, nothing served")

	reg.record("", iterationFact{Kind: iterKindMIParallel, Total: 3})
	require.Nil(t, reg.snapshot(),
		"an unkeyed account is dropped, not stored under an empty key")

	reg.record("act", iterationFact{Kind: iterKindMIParallel, Total: 3})
	got := reg.snapshot()
	require.Len(t, got, 1)
	require.Equal(t, 3, got["act"].Total)

	// a later report of the same activity replaces the earlier one, so the
	// register always holds the latest — and, once it ends, final — account.
	reg.record("act", iterationFact{Kind: iterKindMIParallel, Total: 3,
		Completed: 2, Terminated: 1})
	got = reg.snapshot()
	require.Equal(t, 2, got["act"].Completed)
	require.Equal(t, 1, got["act"].Terminated)
}

// TestStandardLoopPublishesItsOwnVars (SRD-090.D FR-3): a Standard Loop
// publishes the same engine names as a Multi-Instance, and reports its own
// shape.
//
// Worth its own case because a Standard Loop reaches the publication through a
// third path (bindLoopCounterAt, not the MI binder), and because the guide
// tells a reader ITERATION_MODE can read "std_loop" — a claim that should be
// pinned rather than asserted.
func TestStandardLoopPublishesItsOwnVars(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var (
		mu   sync.Mutex
		seen []map[string]string
	)

	op, err := gooper.New("sl-vars-op",
		func(ctx context.Context, r service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			got := map[string]string{}

			for _, n := range []string{
				data.LoopCounterName, IterationNumber, IterationMode,
			} {
				d, gerr := r.GetData(n)
				if gerr != nil {
					return nil, fmt.Errorf("read %q: %w", n, gerr)
				}

				got[n] = fmt.Sprint(d.Value().Get(ctx))
			}

			mu.Lock()
			seen = append(seen, got)
			mu.Unlock()

			return data.MustItemDefinition(
				values.NewVariable("ok"), foundation.WithID("res")), nil
		})
	require.NoError(t, err)

	sl, err := activities.NewStandardLoop(loopCondLt(t, 3))
	require.NoError(t, err)

	p, err := process.New("sl-vars", foundation.WithID("sl-vars"))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start", foundation.WithID("slv-start"))
	require.NoError(t, err)

	work, err := activities.NewServiceTask("work", op,
		activities.WithoutParams(), activities.WithLoop(sl),
		foundation.WithID("slv-work"))
	require.NoError(t, err)

	end, err := events.NewEndEvent("end", foundation.WithID("slv-end"))
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

	<-inst.Done()

	mu.Lock()
	defer mu.Unlock()

	require.NotEmpty(t, seen, "the loop ran at least one pass")

	for i, got := range seen {
		require.Equal(t, iterKindStdLoop, got[IterationMode],
			"a Standard Loop reports its own shape, not a Multi-Instance one")
		require.Equal(t, got[data.LoopCounterName], got[IterationNumber],
			"pass %d: the two spellings of the ordinal agree", i)
	}
}
