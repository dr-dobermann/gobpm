package activities

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRcvTaskOption covers rcvTaskConfig.Validate (no-op) and RcvTaskOption:
// WithInstantiate sets the flag when the option func is applied directly (the
// dispatch path NewReceiveTask uses).
func TestRcvTaskOption(t *testing.T) {
	require.NoError(t, (&rcvTaskConfig{}).Validate())

	var rc rcvTaskConfig
	WithInstantiate()(&rc)
	require.True(t, rc.instantiate)
}
