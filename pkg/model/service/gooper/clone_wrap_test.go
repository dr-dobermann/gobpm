package gooper

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/errs"
)

// TestCloneWrap covers FIX-026: the Go-operation clone-failure classifier
// carries the part and operation name.
func TestCloneWrap(t *testing.T) {
	err := cloneWrap("out", "hello", errs.New(errs.M("inner")))
	require.Error(t, err)
	require.Contains(t, err.Error(), "out")
	require.Contains(t, err.Error(), "hello")
}
