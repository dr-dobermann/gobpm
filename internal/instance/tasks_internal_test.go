package instance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/stretchr/testify/require"
)

// stubActor is a minimal hi.Actor for argument-validation tests.
type stubActor struct{ id string }

func (a stubActor) UserID() string   { return a.id }
func (a stubActor) Groups() []string { return nil }

// TestCheckTaskArgs covers the public Take/Complete argument guards.
func TestCheckTaskArgs(t *testing.T) {
	require.Error(t, checkTaskArgs("", stubActor{id: "x"})) // empty task id
	require.Error(t, checkTaskArgs("id", nil))              // nil actor
	require.NoError(t, checkTaskArgs("id", stubActor{id: "x"}))
}

// TestDenyByResolutionFailure pins the fail-closed rule of SRD-073 FR-5e / §4.8: an
// unresolvable triad must authorize NOBODY. The zero Eligibility would read as an
// open task — no triad declared — and silently authorize every actor, so this is the
// difference between a stuck task and an authorization bypass.
func TestDenyByResolutionFailure(t *testing.T) {
	inst := &Instance{EngineRuntime: enginert.Default()}

	node, err := activities.NewUserTask("approve",
		activities.WithOutput("result", "string", true),
		activities.WithoutParams())
	require.NoError(t, err)

	got := inst.denyByResolutionFailure("task-1", node, errors.New("no frame"))

	require.False(t, got.Open(),
		"a failed resolution must never read as an open task")

	for _, a := range []failActor{
		{id: "john"},
		{id: "anybody", groups: []string{"admins"}},
	} {
		require.Error(t, got.Authorize("task-1", a),
			"a failed resolution must deny every actor")
	}
}

// failActor is a test hi.Actor for the fail-closed check.
type failActor struct {
	id     string
	groups []string
}

func (a failActor) UserID() string   { return a.id }
func (a failActor) Groups() []string { return a.groups }

// TestRuntimeVarCompletedBy covers the performer register's exposure through the
// reserved RUNTIME subtree (ADR-020 v.2 §2.4.2): one map-valued variable, node name →
// the user who completed that task, so the runtime name set stays closed as tasks
// complete.
func TestRuntimeVarCompletedBy(t *testing.T) {
	// The runtime-var builders wrap values in an ItemAwareElement, which needs the
	// default data states the engine installs at startup.
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()
	inst := &Instance{
		EngineRuntime: enginert.Default(),
		performers:    newPerformers(),
	}

	t.Run("empty register reads as an empty map", func(t *testing.T) {
		d, err := inst.RuntimeVar(CompletedBy)
		require.NoError(t, err)

		m, ok := d.Value().Get(ctx).(map[string]string)
		require.True(t, ok, "the register must read as a string map")
		require.Empty(t, m)
	})

	t.Run("records are readable by node name", func(t *testing.T) {
		inst.performers.record("approve", "alice")
		inst.performers.record("countersign", "bob")

		d, err := inst.RuntimeVar(CompletedBy)
		require.NoError(t, err)

		m, _ := d.Value().Get(ctx).(map[string]string)
		require.Equal(t, "alice", m["approve"])
		require.Equal(t, "bob", m["countersign"])
	})

	t.Run("a later completion overwrites its node's entry", func(t *testing.T) {
		// A looped or multi-instance task completes more than once; the register
		// names the LAST completer.
		inst.performers.record("approve", "carol")

		d, err := inst.RuntimeVar(CompletedBy)
		require.NoError(t, err)

		m, _ := d.Value().Get(ctx).(map[string]string)
		require.Equal(t, "carol", m["approve"])
	})

	t.Run("restore adopts a checkpoint's records", func(t *testing.T) {
		// Without this the register would be lost on every hydrate — and a human
		// task is the wait most likely to dehydrate.
		fresh := &Instance{
			EngineRuntime: enginert.Default(),
			performers:    newPerformers(),
		}
		fresh.performers.restore(map[string]string{"approve": "dave"})

		d, err := fresh.RuntimeVar(CompletedBy)
		require.NoError(t, err)

		m, _ := d.Value().Get(ctx).(map[string]string)
		require.Equal(t, "dave", m["approve"])
	})

	t.Run("an empty register snapshots as nil, not an empty map", func(t *testing.T) {
		require.Nil(t, newPerformers().snapshot(),
			"an empty map must not reach the checkpoint wire")
	})
}

// TestStartedAtSurvivesRestore pins the instance's ORIGINAL start time across a
// rebuild. A hydrated instance is the same logical instance, so restamping it to
// "now" would make RUNTIME/STARTED_AT report the age of the latest rebuild — losing
// exactly the interval that mattered, the long wait that caused the dehydration.
func TestStartedAtSurvivesRestore(t *testing.T) {
	began := time.Date(2026, 7, 30, 9, 0, 0, 0, time.UTC)

	t.Run("a recorded stamp is adopted", func(t *testing.T) {
		inst := &Instance{EngineRuntime: enginert.Default()}
		inst.startTime = time.Now()

		inst.restoreStartedAt(began.Format(time.RFC3339Nano))
		require.True(t, inst.startTime.Equal(began))
	})

	t.Run("round-trips through the checkpoint form", func(t *testing.T) {
		inst := &Instance{EngineRuntime: enginert.Default()}
		inst.startTime = began

		stamp := inst.startedAtRFC3339()
		require.NotEmpty(t, stamp)

		rebuilt := &Instance{EngineRuntime: enginert.Default()}
		rebuilt.restoreStartedAt(stamp)
		require.True(t, rebuilt.startTime.Equal(began))
	})

	t.Run("an unstarted instance records nothing", func(t *testing.T) {
		require.Empty(t, (&Instance{}).startedAtRFC3339())
	})

	t.Run("an older checkpoint leaves the rebuild's stamp alone", func(t *testing.T) {
		// The field is absent from checkpoints written before it existed; zeroing
		// the clock would be worse than keeping the rebuild's own stamp.
		inst := &Instance{EngineRuntime: enginert.Default()}
		inst.startTime = began

		inst.restoreStartedAt("")
		require.True(t, inst.startTime.Equal(began))
	})

	t.Run("an unparsable stamp is logged, not fatal", func(t *testing.T) {
		inst := &Instance{EngineRuntime: enginert.Default()}
		inst.startTime = began

		inst.restoreStartedAt("not-a-time")
		require.True(t, inst.startTime.Equal(began),
			"a corrupt stamp must not zero the clock")
	})
}
