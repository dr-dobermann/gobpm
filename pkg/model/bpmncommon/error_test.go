package bpmncommon_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/stretchr/testify/require"
)

// TestErrorStructureNilSafe pins FIX-010 §3.2.2: a BPMN Error's ItemDefinition
// is optional (NewError accepts a nil structure), so Structure() must return nil
// rather than dereference a nil pointer and panic.
func TestErrorStructureNilSafe(t *testing.T) {
	e, err := bpmncommon.NewError("boom", "E1", nil)
	require.NoError(t, err)
	require.NotNil(t, e)

	require.NotPanics(t, func() {
		require.Nil(t, e.Structure())
	})
}
