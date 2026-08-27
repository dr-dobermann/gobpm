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

	require.NoError(t, data.CreateDefaultStates())

	mi, err := activities.NewMultiInstance(
		activities.WithInputCollection("items", "item"))
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
		activities.WithCandidateUsers("alice"),
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

			for _, in := range it.Instances {
				if in.TaskID == "" {
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
// request, each instance takes back the id it was announced under, and a
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

// TestCancellingAnInstanceEndsAnOutstandingFanOut: the decorator is parked
// waiting for approvals nobody has given, and the instance is torn down — it
// must unwind rather than hold its goroutine, and the work it was offering
// must stop being offered.
//
// A task nobody will accept has to be withdrawn, or a person acts on work the
// engine has already discarded (ADR-025 §2.15a).
func TestCancellingAnInstanceEndsAnOutstandingFanOut(t *testing.T) {
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
