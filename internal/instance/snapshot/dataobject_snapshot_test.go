package snapshot_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	dataobjects "github.com/dr-dobermann/gobpm/pkg/model/data_objects"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// TestSnapshotClonesDataObjects: the Snapshot carries the Process DataObjects,
// and each instance clone owns a private copy — mutating one clone's DataObject
// leaves sibling clones and the template untouched (SRD-063 FR-2).
func TestSnapshotClonesDataObjects(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	idef, err := data.NewItemDefinition(values.NewVariable(100))
	require.NoError(t, err)
	do, err := dataobjects.New("order", idef, data.ReadyDataState)
	require.NoError(t, err)

	p, err := process.New("p")
	require.NoError(t, err)
	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, end, do} {
		require.NoError(t, p.Add(e))
	}
	_, err = flow.Link(start, end)
	require.NoError(t, err)

	s, err := snapshot.New(p)
	require.NoError(t, err)
	require.Len(t, s.DataObjects, 1)

	c1, err := s.Clone()
	require.NoError(t, err)
	c2, err := s.Clone()
	require.NoError(t, err)
	require.Len(t, c1.DataObjects, 1)
	require.Len(t, c2.DataObjects, 1)

	require.NoError(t,
		c1.DataObjects[0].ItemDefinition().Structure().Update(t.Context(), 777))
	require.EqualValues(t, 777,
		c1.DataObjects[0].ItemDefinition().Structure().Get(t.Context()))
	require.EqualValues(t, 100,
		c2.DataObjects[0].ItemDefinition().Structure().Get(t.Context()),
		"a sibling clone owns a private DataObject")
	require.EqualValues(t, 100,
		s.DataObjects[0].ItemDefinition().Structure().Get(t.Context()),
		"the snapshot template is unaffected")
}
