package activities

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/stretchr/testify/require"
)

// TestUserTaskCloneRejectsValueLessProperty covers the FIX-017 defensive error
// branch in UserTask.Clone. NewUserTask does not accept property options today
// (see docs/backlog.md — property configuration missing on some Activity/Event
// constructors), so a value-less property is injected directly to exercise the
// guard, the same way the snapshot package tests a guard its constructor
// prevents.
func TestUserTaskCloneRejectsValueLessProperty(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ut := &UserTask{}
	ut.properties = map[string]*data.Property{
		"empty": data.MustProperty("empty",
			data.MustItemDefinition(nil), data.UnavailableDataState),
	}

	_, err := ut.Clone()
	require.Error(t, err)
}
