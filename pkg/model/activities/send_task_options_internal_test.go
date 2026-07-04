package activities

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/stretchr/testify/require"
)

// TestSndTaskOption covers sndTaskConfig.Validate (no-op) and SndTaskOption:
// WithCorrelationKey sets the key when the option func is applied directly (the
// dispatch path NewSendTask uses).
func TestSndTaskOption(t *testing.T) {
	require.NoError(t, (&sndTaskConfig{}).Validate())

	key := &bpmncommon.CorrelationKey{Name: "orderKey"}

	var sc sndTaskConfig
	WithCorrelationKey(key)(&sc)
	require.Same(t, key, sc.correlationKey)
}
