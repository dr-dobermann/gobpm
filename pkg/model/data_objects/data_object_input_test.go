package dataobjects_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	dataobjects "github.com/dr-dobermann/gobpm/pkg/model/data_objects"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/require"
)

// TestAssociateTargetInput — SRD-094 FR-7: the id-addressed target form
// wires the data object into the node's input named by id — an event's
// input carries its definition's item, so the item-addressed form cannot
// find it — and refuses an unknown id and a nil node.
func TestAssociateTargetInput(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	do, err := dataobjects.New("order",
		data.MustItemDefinition(values.NewVariable(""),
			foundation.WithID("order-item")),
		nil)
	require.NoError(t, err)

	end, err := events.NewEndEvent("e",
		events.WithMessageTrigger(events.MustMessageEventDefinition(
			bpmncommon.MustMessage("m",
				data.MustItemDefinition(values.NewVariable(""),
					foundation.WithID("m-item"))),
			nil)))
	require.NoError(t, err)

	require.ErrorContains(t, do.AssociateTarget(end, nil), "has no input #order-item",
		"by item, the event's input is invisible")
	require.ErrorContains(t, do.AssociateTargetInput(end, "nope", nil),
		`has no input "nope"`)
	require.Error(t, do.AssociateTargetInput(nil, "x", nil))

	require.NoError(t, do.AssociateTargetInput(end, end.Inputs()[0].ID(), nil))

	aa := end.InputAssociations()
	require.Len(t, aa, 1)
	require.Equal(t, []string{"order"}, aa[0].SourceNames())
	require.Equal(t, "m-item", aa[0].TargetItemDefID())

	require.ErrorContains(t, do.AssociateTargetInput(end, end.Inputs()[0].ID(), nil),
		"duplicate association")
}
