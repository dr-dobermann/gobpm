package events_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/events"
)

// TestNewBpmnError (SRD-029 §3.5): an empty code is rejected; a valid code carries
// the optional cause through Unwrap and self-identifies by code in Error().
func TestNewBpmnError(t *testing.T) {
	_, err := events.NewBpmnError("  ", nil)
	require.Error(t, err, "an empty error code is rejected")

	cause := errors.New("downstream boom")

	be, err := events.NewBpmnError("E1", cause)
	require.NoError(t, err)
	require.Equal(t, "E1", be.Code)
	require.ErrorIs(t, be, cause, "the cause is reachable via Unwrap")
	require.Contains(t, be.Error(), "E1")
	require.Contains(t, be.Error(), "downstream boom")

	bare, err := events.NewBpmnError("E2", nil)
	require.NoError(t, err)
	require.Contains(t, bare.Error(), "E2")
	require.Nil(t, bare.Unwrap(), "no cause → Unwrap is nil")
}
