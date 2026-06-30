package snapshot

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
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
