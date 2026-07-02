package snapshot

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/stretchr/testify/require"
)

// TestCloneRejectsValuelessProperty covers Clone's defensive property-clone
// error path: a snapshot carrying a value-less property fails to clone rather
// than sharing it. New rejects such a property up front (FIX-016), so this can't
// arise through New — the snapshot is built directly to exercise the guard.
func TestCloneRejectsValuelessProperty(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	s := &Snapshot{
		Nodes: map[string]flow.Node{},
		Properties: []*data.Property{
			data.MustProperty("empty",
				data.MustItemDefinition(nil), data.UnavailableDataState),
		},
	}

	_, err := s.Clone()
	require.Error(t, err)
}

// TestCloneRejectsValuelessNodeProperty covers Clone's node-clone error path: a
// snapshot whose node carries a value-less property fails to clone. New rejects
// such a node up front (FIX-017), so the snapshot is built directly — with empty
// process Properties so the node clone, not the process-property clone, is the
// failing step.
func TestCloneRejectsValuelessNodeProperty(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	start, err := events.NewStartEvent("start",
		data.WithProperties(data.MustProperty("empty",
			data.MustItemDefinition(nil), data.UnavailableDataState)))
	require.NoError(t, err)

	s := &Snapshot{
		Nodes: map[string]flow.Node{start.ID(): start},
	}

	_, err = s.Clone()
	require.Error(t, err)
}
