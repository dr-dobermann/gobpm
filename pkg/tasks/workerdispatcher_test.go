package tasks_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/tasks"
	"github.com/stretchr/testify/require"
)

// TestMakeJobIDRoundTrip: a minted JobID embeds its instance id (recoverable via
// InstanceID), carries a unique suffix, and two jobs on the same instance differ.
// A JobID with no separator reports itself whole.
func TestMakeJobIDRoundTrip(t *testing.T) {
	id := tasks.MakeJobID("inst-42")

	require.Equal(t, "inst-42", id.InstanceID())
	require.NotEqual(t, tasks.JobID("inst-42"), id,
		"a minted id carries a unique suffix beyond the instance id")
	require.NotEqual(t, tasks.MakeJobID("inst-42"), tasks.MakeJobID("inst-42"),
		"two jobs on the same instance get distinct ids")

	// a bare id with no separator is its own instance id.
	require.Equal(t, "plain", tasks.JobID("plain").InstanceID())
}
