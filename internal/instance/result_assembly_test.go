package instance

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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
)

// ownerKey keys a result by the person who completed that instance — the
// motivating case for evaluating the key in the completing instance's own
// frame, since an assignee is not known until the task is claimed.
func ownerKey(t *testing.T) data.FormalExpression {
	t.Helper()

	return goexpr.Must(nil,
		data.MustItemDefinition(values.NewVariable("")),
		func(ctx context.Context, src data.Source) (data.Value, error) {
			d, err := src.Find(ctx, "result")
			if err != nil {
				return values.NewVariable(""), nil
			}

			return values.NewVariable(d.Value().Get(ctx)), nil
		})
}

// resultProc builds start → PARALLEL Multi-Instance User Task (three items) →
// end, with whatever result strategy is asked for.
func resultProc(
	t *testing.T, key string, opts ...activities.MultiInstanceOption,
) *snapshot.Snapshot {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	all := append([]activities.MultiInstanceOption{
		activities.WithInputCollection("items", "item"),
	}, opts...)

	mi, err := activities.NewMultiInstance(all...)
	require.NoError(t, err)

	items := data.MustProperty("items",
		data.MustItemDefinition(values.NewArray("a", "b", "c"),
			foundation.WithID(key+"-items")),
		data.ReadyDataState)

	p, err := process.New(key, foundation.WithID(key),
		data.WithProperties(items))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start", foundation.WithID(key+"-start"))
	require.NoError(t, err)

	ut, err := activities.NewUserTask("approve",
		activities.WithCandidateUsers("alice", "bob", "carol"),
		activities.WithOutput("result", "string", true),
		activities.WithoutParams(), activities.WithLoop(mi),
		foundation.WithID(key+"-approve"))
	require.NoError(t, err)

	end, err := events.NewEndEvent("end", foundation.WithID(key+"-end"))
	require.NoError(t, err)

	for _, e := range []flow.Element{start, ut, end} {
		require.NoError(t, p.Add(e))
	}

	_, err = flow.Link(start, ut)
	require.NoError(t, err)
	_, err = flow.Link(ut, end)
	require.NoError(t, err)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	return s
}

// completeAll runs the fan-out and completes every instance, in the order the
// answers are given, returning the finished instance.
func completeAll(
	t *testing.T, s *snapshot.Snapshot, answers ...string,
) *Instance {
	t.Helper()

	dist := &countingDist{}

	inst, err := New(s, scope.EmptyDataPath, cpRuntime(t), laxEP(t), dist)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, inst.Run(ctx))

	require.Eventually(t, func() bool {
		return len(dist.announced()) == len(answers)
	}, 5*time.Second, 10*time.Millisecond,
		"every instance offers its own task")

	ids := dist.announced()

	for i, answer := range answers {
		out := []data.Data{
			data.MustParameter("result",
				data.MustItemAwareElement(
					data.MustItemDefinition(values.NewVariable(answer)),
					data.ReadyDataState)),
		}

		require.NoError(t,
			inst.Complete(ctx, ids[i], stubActor{id: "alice"}, out))
	}

	return inst
}

// TestADeclaredMapKeysByTheCompletingInstancesExpression (SRD-090.D T-11,
// ADR-025 §2.6.1): the key is evaluated in the instance's OWN frame, at its
// completion, so it can use something that instance produced.
func TestADeclaredMapKeysByTheCompletingInstancesExpression(t *testing.T) {
	s := resultProc(t, "rs-map",
		activities.WithResultMap("byAnswer", "result", ownerKey(t)))

	inst := completeAll(t, s, "yes", "no", "maybe")

	require.Eventually(t, func() bool { return inst.State() == Completed },
		5*time.Second, 10*time.Millisecond)

	ctx := context.Background()

	d, err := inst.sc.plane.GetData(inst.sc.root, "byAnswer")
	require.NoError(t, err, "the assembled map is published at the host scope")

	got, ok := d.Value().Get(ctx).(map[string]any)
	require.True(t, ok)
	require.Equal(t,
		map[string]any{"yes": "yes", "no": "no", "maybe": "maybe"}, got,
		"each instance's result under the key that instance computed")
}

// TestADuplicateResultKeyOverwritesUnlessDeclaredAFault (T-12): overwriting is
// consistent with the last-wins default rather than an exception to it, and the
// loss is detectable — RUNTIME/ITERATIONS publishes the instance total, so a
// map holding fewer entries than that says so.
func TestADuplicateResultKeyOverwritesUnlessDeclaredAFault(t *testing.T) {
	t.Run("overwrites by default", func(t *testing.T) {
		s := resultProc(t, "rs-dup",
			activities.WithResultMap("byAnswer", "result", ownerKey(t)))

		inst := completeAll(t, s, "same", "same", "other")

		require.Eventually(t, func() bool { return inst.State() == Completed },
			5*time.Second, 10*time.Millisecond)

		ctx := context.Background()

		d, err := inst.sc.plane.GetData(inst.sc.root, "byAnswer")
		require.NoError(t, err)

		got, ok := d.Value().Get(ctx).(map[string]any)
		require.True(t, ok)
		require.Len(t, got, 2,
			"three instances, two keys — and RUNTIME/ITERATIONS still says "+
				"three, which is what makes the loss detectable")
	})

	t.Run("faults under ErrorOnKeyRewrite", func(t *testing.T) {
		s := resultProc(t, "rs-dup-err",
			activities.WithResultMap("byAnswer", "result", ownerKey(t),
				activities.ErrorOnKeyRewrite()))

		inst := completeAll(t, s, "same", "same", "other")

		require.Eventually(t, func() bool { return inst.OpenIncidents() == 1 },
			5*time.Second, 10*time.Millisecond,
			"a collision the model declared to be an error IS one: it faults "+
				"the activity rather than quietly keeping the later answer")

		require.NotEqual(t, Completed, inst.State(),
			"and the activity does not complete on a result it was told not "+
				"to assemble that way")
	})
}

// TestADeclaredResultIsInvisibleUntilCompletion (SRD-090.D T-13, ADR-025 §2.6
// as extended by §2.6.1): the assembled collection publishes ONCE, at activity
// completion, never incrementally.
//
// The barrier is what lets a concurrent activity read the name at all: a
// half-assembled collection is a wrong answer that looks like a right one, and
// the spec only RECOMMENDS the collection be inaccessible — this engine makes
// it a guarantee.
func TestADeclaredResultIsInvisibleUntilCompletion(t *testing.T) {
	s := resultProc(t, "rs-barrier",
		activities.WithResultMap("byAnswer", "result", ownerKey(t)))

	dist := &countingDist{}

	inst, err := New(s, scope.EmptyDataPath, cpRuntime(t), laxEP(t), dist)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, inst.Run(ctx))

	require.Eventually(t, func() bool { return len(dist.announced()) == 3 },
		5*time.Second, 10*time.Millisecond)

	ids := dist.announced()

	answer := func(v string) []data.Data {
		return []data.Data{
			data.MustParameter("result",
				data.MustItemAwareElement(
					data.MustItemDefinition(values.NewVariable(v)),
					data.ReadyDataState)),
		}
	}

	// two of three done — the third approval is still outstanding
	require.NoError(t, inst.Complete(ctx, ids[0], stubActor{id: "alice"},
		answer("yes")))
	require.NoError(t, inst.Complete(ctx, ids[1], stubActor{id: "bob"},
		answer("no")))

	require.Never(t, func() bool {
		_, gErr := inst.sc.plane.GetData(inst.sc.root, "byAnswer")

		return gErr == nil
	}, 300*time.Millisecond, 25*time.Millisecond,
		"nothing may read a collection two instances into three")

	require.NoError(t, inst.Complete(ctx, ids[2], stubActor{id: "carol"},
		answer("maybe")))

	require.Eventually(t, func() bool {
		_, gErr := inst.sc.plane.GetData(inst.sc.root, "byAnswer")

		return gErr == nil
	}, 5*time.Second, 10*time.Millisecond,
		"and once every instance has contributed, it is there")
}

// TestAStandardLoopAssemblesItsPassesByOrdinal (SRD-090.D T-10, ADR-025
// §2.6.1): slot i holds pass i's result.
//
// An ENGINE EXTENSION for this shape — BPMN gives a Standard Loop no output
// aggregation at all — which is why the option exists here and a
// Multi-Instance keeps the standard's own `loopDataOutputRef` assembly instead.
func TestAStandardLoopAssemblesItsPassesByOrdinal(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	sl, err := activities.NewStandardLoop(loopCondLt(t, 3),
		activities.WithLoopResultArray("perPass", "result"))
	require.NoError(t, err)

	p, err := process.New("rs-sl", foundation.WithID("rs-sl"))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start", foundation.WithID("rs-sl-start"))
	require.NoError(t, err)

	ut, err := activities.NewUserTask("approve",
		activities.WithCandidateUsers("alice", "bob", "carol"),
		activities.WithOutput("result", "string", true),
		activities.WithoutParams(), activities.WithLoop(sl),
		foundation.WithID("rs-sl-approve"))
	require.NoError(t, err)

	end, err := events.NewEndEvent("end", foundation.WithID("rs-sl-end"))
	require.NoError(t, err)

	for _, e := range []flow.Element{start, ut, end} {
		require.NoError(t, p.Add(e))
	}

	_, err = flow.Link(start, ut)
	require.NoError(t, err)
	_, err = flow.Link(ut, end)
	require.NoError(t, err)

	snap, err := snapshot.New(p)
	require.NoError(t, err)

	dist := &countingDist{}

	inst, err := New(snap, scope.EmptyDataPath, cpRuntime(t), laxEP(t), dist)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, inst.Run(ctx))

	for pass, v := range []string{"first", "second", "third"} {
		require.Eventually(t, func() bool {
			return len(dist.announced()) == pass+1
		}, 5*time.Second, 10*time.Millisecond)

		out := []data.Data{
			data.MustParameter("result",
				data.MustItemAwareElement(
					data.MustItemDefinition(values.NewVariable(v)),
					data.ReadyDataState)),
		}

		require.NoError(t, inst.Complete(ctx,
			dist.announced()[pass], stubActor{id: "alice"}, out))
	}

	require.Eventually(t, func() bool { return inst.State() == Completed },
		5*time.Second, 10*time.Millisecond)

	d, err := inst.sc.plane.GetData(inst.sc.root, "perPass")
	require.NoError(t, err)

	arr, ok := d.Value().(*values.Array[any])
	require.True(t, ok, "an array strategy publishes a collection")

	require.Equal(t,
		[]any{"first", "second", "third"}, arr.GetAll(context.Background()),
		"slot i holds pass i's result, in pass order")
}
