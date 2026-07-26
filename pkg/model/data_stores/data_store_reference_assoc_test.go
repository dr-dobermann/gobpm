package datastores_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	datastores "github.com/dr-dobermann/gobpm/pkg/model/data_stores"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/stretchr/testify/require"
)

// ioTask builds a service task with one input (inID) and one output (outID) —
// an AssociationSource/AssociationTarget for the reference association tests.
func ioTask(t *testing.T, inID, outID string) *activities.ServiceTask {
	t.Helper()

	op, err := gooper.New("op",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			return nil, nil
		})
	require.NoError(t, err)

	st, err := activities.NewServiceTask("io", op,
		activities.WithParameters(data.Input, data.MustParameter("in",
			data.MustItemAwareElement(
				data.MustItemDefinition(
					values.NewVariable(0), foundation.WithID(inID)),
				data.ReadyDataState))),
		activities.WithParameters(data.Output, data.MustParameter("out",
			data.MustItemAwareElement(
				data.MustItemDefinition(
					values.NewVariable(0), foundation.WithID(outID)),
				nil))))
	require.NoError(t, err)

	return st
}

// TestDataStoreReferenceAssociations covers AssociateSource/AssociateTarget
// (SRD-068 FR-3/FR-4): the association a reference builds carries its
// dataStoreRef and binds to the node.
func TestDataStoreReferenceAssociations(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	newRef := func(name, id string) *datastores.DataStoreReference {
		r, err := datastores.New(name, "orders",
			data.MustItemDefinition(
				values.NewVariable(0), foundation.WithID(id)),
			data.ReadyDataState)
		require.NoError(t, err)

		return r
	}

	// xform is a real FormalExpression to drive the optional-transformation
	// branch of both associate paths.
	xform := func(t *testing.T) data.FormalExpression {
		t.Helper()

		te, err := data.NewTextExpression("gobpm:lite", "in")
		require.NoError(t, err)

		return te
	}

	t.Run("associate source (output)", func(t *testing.T) {
		require.NoError(t,
			newRef("total", "out-x").AssociateSource(
				ioTask(t, "in-x", "out-x"), []string{"out-x"}, nil))
	})

	t.Run("associate source with transformation", func(t *testing.T) {
		require.NoError(t,
			newRef("total", "out-x").AssociateSource(
				ioTask(t, "in-x", "out-x"), []string{"out-x"}, xform(t)))
	})

	t.Run("associate target with transformation", func(t *testing.T) {
		require.NoError(t,
			newRef("seed", "in-y").AssociateTarget(
				ioTask(t, "in-y", "out-y"), xform(t)))
	})

	t.Run("associate source: nil node", func(t *testing.T) {
		require.Error(t, newRef("total", "out-x").AssociateSource(nil, nil, nil))
	})

	t.Run("associate source: missing output id", func(t *testing.T) {
		require.Error(t,
			newRef("total", "out-x").AssociateSource(
				ioTask(t, "in-x", "out-x"), []string{"nope"}, nil))
	})

	t.Run("associate target (input)", func(t *testing.T) {
		require.NoError(t,
			newRef("seed", "in-y").AssociateTarget(ioTask(t, "in-y", "out-y"), nil))
	})

	t.Run("associate target: nil node", func(t *testing.T) {
		require.Error(t, newRef("seed", "in-y").AssociateTarget(nil, nil))
	})

	t.Run("associate target: no matching input", func(t *testing.T) {
		require.Error(t,
			newRef("seed", "other").AssociateTarget(ioTask(t, "in-y", "out-y"), nil))
	})

	t.Run("associate target: duplicate", func(t *testing.T) {
		r := newRef("seed", "in-y")
		tsk := ioTask(t, "in-y", "out-y")
		require.NoError(t, r.AssociateTarget(tsk, nil))
		require.Error(t, r.AssociateTarget(tsk, nil))
	})
}
