package tasks_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/tasks"
	"github.com/stretchr/testify/require"
)

// TestTrustModeString covers the mode→name table used for logging.
func TestTrustModeString(t *testing.T) {
	var unset tasks.TrustMode

	require.Equal(t, "unset", unset.String())
	require.Equal(t, "workerTrusted", tasks.WorkerTrusted.String())
	require.Equal(t, "engineAuthoritative", tasks.EngineAuthoritative.String())
	require.Equal(t, "unknown", tasks.TrustMode(99).String())
}

// TestTrustModeResolve covers the two-level resolution helper: an unset mode
// takes the fallback, a set mode keeps itself.
func TestTrustModeResolve(t *testing.T) {
	var unset tasks.TrustMode

	require.Equal(t, tasks.WorkerTrusted, unset.Resolve(tasks.WorkerTrusted))
	require.Equal(t, tasks.EngineAuthoritative,
		tasks.EngineAuthoritative.Resolve(tasks.WorkerTrusted))

	// per-service over engine-wide over the default.
	require.Equal(t, tasks.EngineAuthoritative,
		unset.Resolve(tasks.EngineAuthoritative.Resolve(tasks.WorkerTrusted)))
}
