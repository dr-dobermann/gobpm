package activities_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/stretchr/testify/require"
)

func TestManualTask(t *testing.T) {
	mt, err := activities.NewManualTask("ack", activities.WithoutParams())
	require.NoError(t, err)

	require.Equal(t, flow.ManualTask, mt.TaskType())
	require.Equal(t, flow.Node(mt), mt.Node())

	// no-op pass-through: Exec binds nothing (re is unused) and returns the
	// outgoing flows — empty for a standalone node.
	flows, err := mt.Exec(context.Background(), nil)
	require.NoError(t, err)
	require.Empty(t, flows)

	// Clone is an independent node carrying the same id.
	cn, err := mt.Clone()
	require.NoError(t, err)
	clone, ok := cn.(*activities.ManualTask)
	require.True(t, ok)
	require.NotSame(t, mt, clone)
	require.Equal(t, mt.ID(), clone.ID())
}

func TestNewManualTaskInvalid(t *testing.T) {
	_, err := activities.NewManualTask("")
	require.Error(t, err)
}
