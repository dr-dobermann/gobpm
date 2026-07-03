package interactor_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/interactor"
	"github.com/stretchr/testify/require"
)

func TestNopDistributor(t *testing.T) {
	d := interactor.NopDistributor()
	require.NotNil(t, d)
	require.NoError(t, d.Distribute(context.Background(), interactor.TaskInfo{}))
	require.NoError(t, d.Withdraw(context.Background(), "any"))
}

func TestTaskRefEmbedding(t *testing.T) {
	ref := interactor.TaskRef{
		TaskID:     "t1",
		InstanceID: "i1",
		NodeID:     "n1",
		ProcessID:  "p1",
	}

	// TaskRef fields are promoted through both TaskInfo and TaskView.
	view := interactor.TaskView{TaskRef: ref}
	require.Equal(t, "t1", view.TaskID)
	require.Equal(t, "i1", view.InstanceID)

	info := interactor.TaskInfo{TaskRef: ref}
	require.Equal(t, "n1", info.NodeID)
	require.Equal(t, "p1", info.ProcessID)
}
