package dataflow

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestOpErr covers the single-line operation-error builder the invariant
// branches ride on.
func TestOpErr(t *testing.T) {
	err := opErr("boom", errors.New("cause"))
	require.ErrorContains(t, err, "boom")
	require.ErrorContains(t, err, "cause")
}
