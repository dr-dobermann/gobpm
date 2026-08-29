package datastores_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	datastores "github.com/dr-dobermann/gobpm/pkg/model/data_stores"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/require"
)

// TestAssociateTargetInput — SRD-094 FR-7: the id-addressed target form
// wires the store reference into an event's input named by id, carrying
// the store ref; an unknown id and a nil node are refused.
func TestAssociateTargetInput(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ref, err := datastores.New("seed", "audit",
		data.MustItemDefinition(values.NewVariable(""),
			foundation.WithID("seed-item")),
		nil)
	require.NoError(t, err)

	end, err := events.NewEndEvent("e",
		events.WithMessageTrigger(events.MustMessageEventDefinition(
			bpmncommon.MustMessage("m",
				data.MustItemDefinition(values.NewVariable(""),
					foundation.WithID("m-item"))),
			nil)))
	require.NoError(t, err)

	require.ErrorContains(t, ref.AssociateTargetInput(end, "nope", nil),
		`has no input "nope"`)
	require.Error(t, ref.AssociateTargetInput(nil, "x", nil))

	require.NoError(t, ref.AssociateTargetInput(end, end.Inputs()[0].ID(), nil))

	aa := end.InputAssociations()
	require.Len(t, aa, 1)
	require.Equal(t, "audit", aa[0].DataStoreRef())
	require.Equal(t, []string{"seed"}, aa[0].SourceNames())
}
