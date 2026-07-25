package process_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	dataobjects "github.com/dr-dobermann/gobpm/pkg/model/data_objects"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// TestProcessRegistersDataObject: a DataObject is registered on the Process via
// Add, exposed by DataObjects(), and its name is unique among DataObjects and
// must not collide with a Property (SRD-063 FR-1/FR-7 — one scope name-space).
func TestProcessRegistersDataObject(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	newDO := func(name string, v int) *dataobjects.DataObject {
		idef, err := data.NewItemDefinition(values.NewVariable(v))
		require.NoError(t, err)
		do, err := dataobjects.New(name, idef, data.ReadyDataState)
		require.NoError(t, err)

		return do
	}

	// a process carrying a property named "dup" (for the collision check).
	pidef, err := data.NewItemDefinition(values.NewVariable(0))
	require.NoError(t, err)
	prop, err := data.NewProperty("dup", pidef, data.ReadyDataState)
	require.NoError(t, err)

	p, err := process.New("p", data.WithProperties(prop))
	require.NoError(t, err)

	require.NoError(t, p.Add(newDO("order", 1)))

	dos := p.DataObjects()
	require.Len(t, dos, 1)
	require.Equal(t, "order", dos[0].Name())

	require.Error(t, p.Add(newDO("order", 2)),
		"a duplicate DataObject name is rejected")
	require.Error(t, p.Add(newDO("dup", 3)),
		"a DataObject name colliding with a property is rejected")

	// an element lying about DataObjectElement but not a *DataObject is rejected.
	require.Error(t,
		p.Add(fakeElement{id: "liar", etype: flow.DataObjectElement}))
}
