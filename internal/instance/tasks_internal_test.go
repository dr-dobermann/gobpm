package instance

import (
	"testing"

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
