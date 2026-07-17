package activities

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCallActivityValidateDefensive — the Validate branches unreachable
// through NewCallActivity (the constructor already rejects both shapes):
// the hook re-asserts them for a hand-built or future-deserialized node.
func TestCallActivityValidateDefensive(t *testing.T) {
	t.Run("empty key", func(t *testing.T) {
		ca := &CallActivity{}
		require.Error(t, ca.Validate())
	})

	t.Run("negative version pin", func(t *testing.T) {
		ca := &CallActivity{calledKey: "billing", calledVersion: -1}
		require.Error(t, ca.Validate())
	})
}

// TestCallActivityParamNamesNilIoSpec — the nil-IoSpec guard, unreachable via
// NewCallActivity (which always builds an IoSpec) but exercised for a
// hand-built node.
func TestCallActivityParamNamesNilIoSpec(t *testing.T) {
	ca := &CallActivity{} // no IoSpec

	require.Nil(t, ca.CallInputs())
	require.Nil(t, ca.CallOutputs())
}
