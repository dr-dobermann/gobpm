package activities_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/lanes"
)

// TestNewSubProcessPropagatesLaneOptionErrors pins that a failing lane-set
// option refuses the Sub-Process rather than being swallowed: the option's
// own error is what the caller gets.
func TestNewSubProcessPropagatesLaneOptionErrors(t *testing.T) {
	_, err := activities.NewSubProcess("sp", lanes.WithLaneSets(nil))
	require.Error(t, err)
	require.Contains(t, err.Error(), "WithLaneSets")
}
