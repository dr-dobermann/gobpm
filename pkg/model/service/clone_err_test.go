package service

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/errs"
)

// TestCloneErr covers FIX-026: the clone-failure classifier carries the part
// and operation name.
func TestCloneErr(t *testing.T) {
	err := cloneErr("in", "my-op", errs.New(errs.M("inner")))
	require.Error(t, err)
	require.Contains(t, err.Error(), "in")
	require.Contains(t, err.Error(), "my-op")
}
