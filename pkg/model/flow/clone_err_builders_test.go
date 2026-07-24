package flow

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/errs"
)

// TestCloneErrBuilders covers FIX-026: the clone-graph failure classifiers
// carry the ids the operator needs.
func TestCloneErrBuilders(t *testing.T) {
	err := cloneFlowErr("f-1", errs.New(errs.M("inner")))
	require.Contains(t, err.Error(), "f-1")
	require.Contains(t, err.Error(), "cloned flow")

	err = defaultFlowErr("n-1", "f-2", errs.New(errs.M("inner")))
	require.Contains(t, err.Error(), "n-1")
	require.Contains(t, err.Error(), "f-2")
}
