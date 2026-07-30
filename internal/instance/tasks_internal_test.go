package instance

import (
	"errors"
	"testing"

	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
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
