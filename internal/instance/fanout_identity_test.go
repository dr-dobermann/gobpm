package instance

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
)

// fanOutSnapshot builds start → PARALLEL Multi-Instance over a User Task
// (three items) → end: three approvals offered at once, which is the construct
// ADR-025 §2.15 exists for.
func fanOutSnapshot(t *testing.T, key string) *snapshot.Snapshot {
	t.Helper()

	return miUserTaskSnapshot(t, key, false)
}

// miUserTaskSnapshot builds start → Multi-Instance User Task (three items) →
// end, in whichever shape is asked for.
func miUserTaskSnapshot(
	t *testing.T, key string, sequential bool,
) *snapshot.Snapshot {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	opts := []activities.MultiInstanceOption{
		activities.WithInputCollection("items", "item"),
	}
	if sequential {
		opts = append(opts, activities.WithSequential())
	}

	mi, err := activities.NewMultiInstance(opts...)
	require.NoError(t, err)

	return miUserTaskSnapshotWith(t, key, mi)
}

// miUserTaskSnapshotWith is the same process over a Multi-Instance the caller
// prepared — for a test that needs one carrying a result strategy.
func miUserTaskSnapshotWith(
	t *testing.T, key string, mi *activities.MultiInstanceLoopCharacteristics,
) *snapshot.Snapshot {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

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
		// three candidates, so a test can have a DIFFERENT person do each
		// iteration — the case one entry per node loses.
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

// captureParkedFanOut runs the fan-out to its park and returns the checkpoint
// taken while all three approvals are outstanding.
func captureParkedFanOut(
	t *testing.T, s *snapshot.Snapshot, dist *countingDist,
) *checkpoint.Document {
	t.Helper()

	rt := cpRuntime(t)

	inst, err := New(s, scope.EmptyDataPath, rt, laxEP(t), dist,
		WithCheckpointing("engine-A", "engine-A", time.Minute))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	require.NoError(t, inst.Run(ctx))

	var doc *checkpoint.Document

	require.True(t, assert.Eventually(t, func() bool {
		rec, ok, lErr := rt.Repository().Load(context.Background(), inst.ID())
		if lErr != nil || !ok {
			return false
		}

		d, uErr := checkpoint.Unmarshal(rec.Payload)
		if uErr != nil {
			return false
		}

		for i := range d.Tracks {
			it := d.Tracks[i].Iteration
			if it == nil || len(it.Instances) != 3 {
				continue
			}

			// Require the eligibility verdict too, not just the identity:
			// the announcement records them in that order, so a checkpoint
			// can carry three TaskIDs and not yet the verdict its readers
			// assert. Polling caught that one under load (#356).
			for _, in := range it.Instances {
				if in.TaskID == "" || in.Eligible == nil {
					return false
				}
			}

			doc = d

			return true
		}

		return false
	}, 5*time.Second, 5*time.Millisecond),
		"the capture must record an identity per instance — without them a "+
			"restore mints fresh ids and every handle a person holds names "+
			"nothing (ADR-020 §2.12)")

	return doc
}

// TestARestoredFanOutIsCompletedOnTheIdentitiesItAnnounced (ADR-020 §2.12,
// SRD-071 FR-8): a released fan-out comes back holding the SAME three tasks,
// each still completable on its own, and the activity finishes only when every
// one of them has actually been done.
//
// The whole chain is under test here: the capture records an identity per
// instance, the restore registers them before the loop serves its first
// request, each iteration takes back the id it was announced under, and a
// completion reaches the instance that owns it.
func TestARestoredFanOutIsCompletedOnTheIdentitiesItAnnounced(t *testing.T) {
	s := fanOutSnapshot(t, "cr-fan")

	dist := &countingDist{}
	doc := captureParkedFanOut(t, s, dist)

	var recorded []string

	for i := range doc.Tracks {
		if it := doc.Tracks[i].Iteration; it != nil {
			for _, in := range it.Instances {
				recorded = append(recorded, in.TaskID)
			}
		}
	}

	require.Len(t, recorded, 3)

	restored, err := Restore(doc, s, scope.EmptyDataPath,
		cpRuntime(t), laxEP(t), dist, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, restored.Run(ctx))

	out := []data.Data{
		data.MustParameter("result",
			data.MustItemAwareElement(
				data.MustItemDefinition(values.NewVariable("approved")),
				data.ReadyDataState)),
	}

	alice := stubActor{id: "alice"}

	// completing TWO must not finish the activity: the third approval is a
	// real piece of outstanding work, and finishing without it is the defect
	// the construct was refused over.
	for _, id := range recorded[:2] {
		require.NoError(t, restored.Complete(ctx, id, alice, out),
			"the task is completable on the identity its holder carries, "+
				"across the release")
	}

	require.Never(t, func() bool { return restored.State() == Completed },
		300*time.Millisecond, 25*time.Millisecond,
		"two of three approvals is not done")

	require.NoError(t, restored.Complete(ctx, recorded[2], alice, out))

	require.Eventually(t, func() bool { return restored.State() == Completed },
		5*time.Second, 10*time.Millisecond,
		"with every instance completed by hand, the activity exits once")
}

// TestCancellingAnIterationEndsAnOutstandingFanOut: the decorator is parked
// waiting for approvals nobody has given, and the instance is torn down — it
// must unwind rather than hold its goroutine, and the work it was offering
// must stop being offered.
//
// A task nobody will accept has to be withdrawn, or a person acts on work the
// engine has already discarded (ADR-025 §2.15a).
func TestCancellingAnIterationEndsAnOutstandingFanOut(t *testing.T) {
	s := fanOutSnapshot(t, "cr-fan-cancel")

	dist := &countingDist{}
	rt := cpRuntime(t)

	inst, err := New(s, scope.EmptyDataPath, rt, laxEP(t), dist)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, inst.Run(ctx))

	require.Eventually(t, func() bool { return len(dist.announced()) == 3 },
		5*time.Second, 10*time.Millisecond,
		"three approvals outstanding at once")

	cancel()

	select {
	case <-inst.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the fan-out never unwound — its decorator is still parked " +
			"on approvals that can no longer arrive")
	}

	require.Equal(t, Terminated, inst.State())
}

// TestAParkedFanOutReleasesItsGoroutines (SRD-071 FR-1): three approvals
// outstanding hold no goroutine.
//
// The waits belong to the distributor's inbox, not to the engine — an activity
// that sits for days waiting on people must not pin a process instance in
// memory for them. The decorator unwinds on the release and the instance
// resumes from its recorded position when somebody acts (ADR-007 v.2.1).
func TestAParkedFanOutReleasesItsGoroutines(t *testing.T) {
	s := fanOutSnapshot(t, "cr-fan-rel")

	dist := &countingDist{}
	rt := cpRuntime(t)

	// a holder is what makes the release possible at all: a released wait
	// must be held by something outside the instance, or nothing could ever
	// wake it (SRD-071 FR-3a).
	inst, err := New(s, scope.EmptyDataPath, rt, laxEP(t), dist,
		WithCheckpointing("engine-A", "engine-A", time.Minute),
		WithWaitHolders(newFakeHolders()))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, inst.Run(ctx))

	require.Eventually(t, func() bool { return len(dist.announced()) == 3 },
		5*time.Second, 10*time.Millisecond,
		"three approvals offered at once")

	require.Eventually(t, func() bool { return inst.State() == Dehydrated },
		5*time.Second, 10*time.Millisecond,
		"and then released — the decorator holds N waits, and a wait nobody "+
			"is working on is not a reason to stay resident")
}

// TestASequentialIteratedUserTaskIsCompletedPassByPass (ADR-025 §2.15): a
// SEQUENTIAL Multi-Instance over a User Task offers ONE task at a time — the
// shape the parallel fan-out was refused in favour of, and the one the
// refusal's message still names.
//
// It parks on the track rather than in the decorator's queue, because one pass
// runs at a time, so the decorator routes its completion there. A fan-out's
// deliveries are queued and applied serially instead; both go through the
// decorator, which is what makes it the node to everything outside it.
func TestASequentialIteratedUserTaskIsCompletedPassByPass(t *testing.T) {
	s := miUserTaskSnapshot(t, "cr-seq-ut", true)

	dist := &countingDist{}

	inst, err := New(s, scope.EmptyDataPath, cpRuntime(t), laxEP(t), dist)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, inst.Run(ctx))

	out := []data.Data{
		data.MustParameter("result",
			data.MustItemAwareElement(
				data.MustItemDefinition(values.NewVariable("approved")),
				data.ReadyDataState)),
	}

	alice := stubActor{id: "alice"}

	for pass := 1; pass <= 3; pass++ {
		require.Eventually(t, func() bool {
			return len(dist.announced()) == pass
		}, 5*time.Second, 10*time.Millisecond,
			"pass %d offers its own task, and only its own", pass)

		ids := dist.announced()
		require.NoError(t, inst.Complete(ctx, ids[pass-1], alice, out),
			"each pass is completed on the identity it was announced under")
	}

	require.Eventually(t, func() bool { return inst.State() == Completed },
		5*time.Second, 10*time.Millisecond,
		"three passes done, and the activity leaves")
}

// TestIterationOwnersAnswersWhoDidWhichInstance (SRD-090.D FR-4, T-9): a
// register of activity id → (ordinal → the actor who completed that instance).
//
// `COMPLETED_BY` cannot answer this. It keys by NODE, so an iterated activity
// has ONE entry however many instances ran and whoever did them — the last
// completion wins and the rest are lost. Three approvals are three pieces of
// work by three people, and which of them approved item 2 has to stay
// answerable after the activity has gone.
func TestIterationOwnersAnswersWhoDidWhichInstance(t *testing.T) {
	s := miUserTaskSnapshot(t, "cr-owners", true)

	dist := &countingDist{}

	inst, err := New(s, scope.EmptyDataPath, cpRuntime(t), laxEP(t), dist)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, inst.Run(ctx))

	out := []data.Data{
		data.MustParameter("result",
			data.MustItemAwareElement(
				data.MustItemDefinition(values.NewVariable("approved")),
				data.ReadyDataState)),
	}

	// a different person does each pass — the case one entry per node loses.
	approvers := []string{"alice", "bob", "carol"}

	for pass, who := range approvers {
		require.Eventually(t, func() bool {
			return len(dist.announced()) == pass+1
		}, 5*time.Second, 10*time.Millisecond)

		ids := dist.announced()
		require.NoError(t,
			inst.Complete(ctx, ids[pass], stubActor{id: who}, out))
	}

	require.Eventually(t, func() bool { return inst.State() == Completed },
		5*time.Second, 10*time.Millisecond)

	// read the way a process reads it: through the reserved RUNTIME source.
	v, err := inst.RuntimeVar(IterationOwners)
	require.NoError(t, err)

	owners, ok := v.Value().Get(ctx).(map[string]map[string]string)
	require.True(t, ok, "a map of activity id → (ordinal → owner)")
	require.Len(t, owners, 1, "one iterated activity ran")

	require.Equal(t,
		map[string]string{"0": "alice", "1": "bob", "2": "carol"},
		owners["cr-owners-approve"],
		"every instance keeps the person who did it, keyed by the ordinal "+
			"ITERATION_NUMBER publishes inside it")

	// and it OUTLIVES the activity: the decorator that held the account is
	// gone by now, which is the whole reason the register is not its state.
	require.Equal(t, Completed, inst.State())
}

// TestIterationOwnersRecordsNothingForNobody: an entry naming no actor would
// answer "who did this" with a blank rather than with silence.
func TestIterationOwnersRecordsNothingForNobody(t *testing.T) {
	o := newIterationOwners()

	require.Nil(t, o.snapshot(), "nobody has completed anything")

	o.record("", 0, "alice")
	o.record("act", 0, "")
	require.Nil(t, o.snapshot())

	o.record("act", 2, "alice")
	require.Equal(t,
		map[string]map[string]string{"act": {"2": "alice"}}, o.snapshot())

	// the snapshot is a COPY, inner map included: a reader must not be able
	// to reach into the register the engine is still writing.
	o.snapshot()["act"]["2"] = "tampered"
	require.Equal(t, "alice", o.snapshot()["act"]["2"])
}

// TestAStandardLoopOverAUserTaskOffersATaskPerPass (ADR-025 §2.13, ADR-020
// §2.12): a loop runs ONE pass at a time, so each pass parks as itself, is
// announced as its own task, and its completion goes to the pass that is
// parked on it — the track's own channel, since only one waits.
//
// It also keeps a completion account: a loop over human work is as much "who
// did which pass" as a Multi-Instance is.
func TestAStandardLoopOverAUserTaskOffersATaskPerPass(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	sl, err := activities.NewStandardLoop(loopCondLt(t, 3))
	require.NoError(t, err)

	p, err := process.New("sl-ut", foundation.WithID("sl-ut"))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start", foundation.WithID("sl-ut-start"))
	require.NoError(t, err)

	ut, err := activities.NewUserTask("approve",
		activities.WithCandidateUsers("alice", "bob", "carol"),
		activities.WithOutput("result", "string", true),
		activities.WithoutParams(), activities.WithLoop(sl),
		foundation.WithID("sl-ut-approve"))
	require.NoError(t, err)

	end, err := events.NewEndEvent("end", foundation.WithID("sl-ut-end"))
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

	dist := &countingDist{}

	inst, err := New(s, scope.EmptyDataPath, cpRuntime(t), laxEP(t), dist)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, inst.Run(ctx))

	out := []data.Data{
		data.MustParameter("result",
			data.MustItemAwareElement(
				data.MustItemDefinition(values.NewVariable("approved")),
				data.ReadyDataState)),
	}

	approvers := []string{"alice", "bob", "carol"}

	for pass, who := range approvers {
		require.Eventually(t, func() bool {
			return len(dist.announced()) == pass+1
		}, 5*time.Second, 10*time.Millisecond,
			"pass %d offers its own task, and only its own", pass)

		ids := dist.announced()
		require.NoError(t,
			inst.Complete(ctx, ids[pass], stubActor{id: who}, out))
	}

	require.Eventually(t, func() bool { return inst.State() == Completed },
		5*time.Second, 10*time.Millisecond,
		"the loop condition stops it after three passes")

	v, err := inst.RuntimeVar(IterationOwners)
	require.NoError(t, err)

	owners, ok := v.Value().Get(ctx).(map[string]map[string]string)
	require.True(t, ok)
	require.Equal(t,
		map[string]string{"0": "alice", "1": "bob", "2": "carol"},
		owners["sl-ut-approve"],
		"each pass keeps the person who did it")
}

// TestIterationOwnersSurvivesTheInstanceBeingRebuilt (SRD-090.D FR-4): who did
// which iteration rides the checkpoint.
//
// It has to. A fan-out over human work exists because N approvals take days,
// so being released and rebuilt is its ordinary state rather than an edge one
// — a register rebuilt empty would answer "nobody did any of it" for exactly
// the workload it was built for. COMPLETED_BY rides the checkpoint for the
// same reason and cannot stand in for this one: it keys by node, so an
// iterated activity has a single entry however many instances ran.
func TestIterationOwnersSurvivesTheInstanceBeingRebuilt(t *testing.T) {
	s := miUserTaskSnapshot(t, "cr-owners-cp", true)

	dist := &countingDist{}
	rt := cpRuntime(t)

	inst, err := New(s, scope.EmptyDataPath, rt, laxEP(t), dist,
		WithCheckpointing("engine-A", "engine-A", time.Minute))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, inst.Run(ctx))

	out := []data.Data{
		data.MustParameter("result",
			data.MustItemAwareElement(
				data.MustItemDefinition(values.NewVariable("approved")),
				data.ReadyDataState)),
	}

	require.Eventually(t, func() bool { return len(dist.announced()) == 1 },
		5*time.Second, 10*time.Millisecond)

	require.NoError(t, inst.Complete(ctx,
		dist.announced()[0], stubActor{id: "alice"}, out))

	// the checkpoint that carries alice's approval
	var doc *checkpoint.Document

	require.Eventually(t, func() bool {
		rec, ok, lErr := rt.Repository().Load(context.Background(), inst.ID())
		if lErr != nil || !ok {
			return false
		}

		d, uErr := checkpoint.Unmarshal(rec.Payload)
		if uErr != nil {
			return false
		}

		if len(d.IterationOwners) == 0 {
			return false
		}

		doc = d

		return true
	}, 5*time.Second, 10*time.Millisecond,
		"the account is captured, not left in memory with the decorator")

	require.Equal(t,
		map[string]string{"0": "alice"},
		doc.IterationOwners["cr-owners-cp-approve"])

	// rebuilt from that document, the answer is still alice's
	restored, err := Restore(doc, s, scope.EmptyDataPath,
		cpRuntime(t), laxEP(t), dist, nil)
	require.NoError(t, err)

	v, err := restored.RuntimeVar(IterationOwners)
	require.NoError(t, err)

	owners, ok := v.Value().Get(ctx).(map[string]map[string]string)
	require.True(t, ok)
	require.Equal(t,
		map[string]string{"0": "alice"},
		owners["cr-owners-cp-approve"],
		"a later node asking who approved item 0 gets alice, not silence")
}

// TestIterationsSurvivesTheInstanceBeingRebuilt (SRD-090.D FR-4, M1b): what an
// activity's iteration DID rides the checkpoint too.
//
// The register exists to be read AFTER the activity — that is the question
// BPMN's counts cannot answer, since they end with the activation they
// describe. A node asking it is therefore reading in a rebuilt iteration
// whenever the process waited on anything, and a register rebuilt empty would
// report an activity that processed three items as having processed none: a
// wrong answer rather than a missing one.
func TestIterationsSurvivesTheInstanceBeingRebuilt(t *testing.T) {
	s := miUserTaskSnapshot(t, "cr-iters-cp", true)

	dist := &countingDist{}
	rt := cpRuntime(t)

	inst, err := New(s, scope.EmptyDataPath, rt, laxEP(t), dist,
		WithCheckpointing("engine-A", "engine-A", time.Minute))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, inst.Run(ctx))

	out := []data.Data{
		data.MustParameter("result",
			data.MustItemAwareElement(
				data.MustItemDefinition(values.NewVariable("approved")),
				data.ReadyDataState)),
	}

	require.Eventually(t, func() bool { return len(dist.announced()) == 1 },
		5*time.Second, 10*time.Millisecond)

	require.NoError(t, inst.Complete(ctx,
		dist.announced()[0], stubActor{id: "alice"}, out))

	var doc *checkpoint.Document

	require.Eventually(t, func() bool {
		rec, ok, lErr := rt.Repository().Load(context.Background(), inst.ID())
		if lErr != nil || !ok {
			return false
		}

		d, uErr := checkpoint.Unmarshal(rec.Payload)
		if uErr != nil {
			return false
		}

		if d.Iterations["cr-iters-cp-approve"].Completed == 0 {
			return false
		}

		doc = d

		return true
	}, 5*time.Second, 10*time.Millisecond,
		"the account is captured, not left in memory with the decorator")

	restored, err := Restore(doc, s, scope.EmptyDataPath,
		cpRuntime(t), laxEP(t), dist, nil)
	require.NoError(t, err)

	v, err := restored.RuntimeVar(Iterations)
	require.NoError(t, err)

	iters, ok := v.Value().Get(ctx).(map[string]iterationFact)
	require.True(t, ok)

	got := iters["cr-iters-cp-approve"]
	require.Equal(t, iterKindMISequential, got.Kind)
	require.Equal(t, 3, got.Total, "three items, frozen at activation")
	require.Equal(t, 1, got.Completed,
		"one approval done — a rebuilt register would say none")
}

// perIterationAssigneeSnapshot is the fan-out the example runs: each iteration's
// assignee IS the element it was seeded with, so the three tasks belong to three
// different people and nobody else may complete them.
func perIterationAssigneeSnapshot(
	t *testing.T, key string,
) *snapshot.Snapshot {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	mi, err := activities.NewMultiInstance(
		activities.WithInputCollection("items", "item"))
	require.NoError(t, err)

	items := data.MustProperty("items",
		data.MustItemDefinition(values.NewArray("alice", "bob", "carol"),
			foundation.WithID(key+"-items")),
		data.ReadyDataState)

	p, err := process.New(key, foundation.WithID(key),
		data.WithProperties(items))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start", foundation.WithID(key+"-start"))
	require.NoError(t, err)

	assignee := goexpr.Must(nil,
		data.MustItemDefinition(values.NewVariable("")),
		func(ctx context.Context, src data.Source) (data.Value, error) {
			d, fErr := src.Find(ctx, "item")
			if fErr != nil {
				return nil, fErr
			}

			return values.NewVariable(d.Value().Get(ctx)), nil
		})

	ut, err := activities.NewUserTask("approve",
		activities.WithAssigneeExpr(assignee),
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

// TestARestoredFanOutKeepsTheEligibilityItAnnounced (ADR-020 §2.7, SRD-090.D
// FR-10): the three people holding these tasks can still complete them after a
// release, and still only their own.
//
// Eligibility is assessed ONCE, at the announcement, in the data of the
// iteration being announced — and a restore has no such data: the element the
// iteration was seeded with is frame-local to an execution that no longer
// exists. Resolving again reads the host's scope, where "item" is not, so
// every assignee resolves to nobody and each of the three is locked out of
// work their inbox is still showing them. The verdict therefore rides the
// checkpoint, and this test is what says so.
func TestARestoredFanOutKeepsTheEligibilityItAnnounced(t *testing.T) {
	s := perIterationAssigneeSnapshot(t, "cr-elig")

	dist := &countingDist{}
	doc := captureParkedFanOut(t, s, dist)

	// who each iteration was announced to, as the capture recorded it.
	byTask := map[string]string{}

	for i := range doc.Tracks {
		it := doc.Tracks[i].Iteration
		if it == nil {
			continue
		}

		for _, in := range it.Instances {
			require.NotNil(t, in.Eligible,
				"the verdict is recorded beside the identity — without it "+
					"the restore has nothing to authorize against")
			require.Len(t, in.Eligible.Assignee, 1)

			byTask[in.TaskID] = in.Eligible.Assignee[0]
		}
	}

	require.Len(t, byTask, 3)

	restored, err := Restore(doc, s, scope.EmptyDataPath,
		cpRuntime(t), laxEP(t), dist, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, restored.Run(ctx))

	out := []data.Data{
		data.MustParameter("result",
			data.MustItemAwareElement(
				data.MustItemDefinition(values.NewVariable("approved")),
				data.ReadyDataState)),
	}

	for id, who := range byTask {
		// the WRONG person is still refused: the restore carried a verdict,
		// not an open door.
		require.Error(t, restored.Complete(ctx, id, stubActor{id: "mallory"}, out),
			"a restored task authorizes the same people it always did")

		require.NoError(t, restored.Complete(ctx, id, stubActor{id: who}, out),
			"and its assignee can still do the work they are holding")
	}

	require.Eventually(t, func() bool { return restored.State() == Completed },
		5*time.Second, 10*time.Millisecond)
}
