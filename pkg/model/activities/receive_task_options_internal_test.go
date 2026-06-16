package activities

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRcvTaskOption covers the rcvTaskConfig Configurator + RcvTaskOption.Apply:
// WithInstantiate sets the flag, Validate is a no-op, and Apply rejects a
// configurator that isn't a *rcvTaskConfig.
func TestRcvTaskOption(t *testing.T) {
	require.NoError(t, (&rcvTaskConfig{}).Validate())

	var rc rcvTaskConfig
	require.NoError(t, WithInstantiate().Apply(&rc))
	require.True(t, rc.instantiate)

	// a configurator of a different type is rejected.
	require.Error(t, WithInstantiate().Apply(&usrTaskConfig{}))
}
