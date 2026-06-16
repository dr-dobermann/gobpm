package activities

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/stretchr/testify/require"
)

// TestSndTaskOption covers the sndTaskConfig Configurator + SndTaskOption.Apply:
// WithCorrelationKey sets the key, Validate is a no-op, and Apply rejects a
// configurator that isn't a *sndTaskConfig.
func TestSndTaskOption(t *testing.T) {
	require.NoError(t, (&sndTaskConfig{}).Validate())

	key := &bpmncommon.CorrelationKey{Name: "orderKey"}

	var sc sndTaskConfig
	require.NoError(t, WithCorrelationKey(key).Apply(&sc))
	require.Same(t, key, sc.correlationKey)

	// a configurator of a different type is rejected.
	require.Error(t, WithCorrelationKey(key).Apply(&rcvTaskConfig{}))
}
