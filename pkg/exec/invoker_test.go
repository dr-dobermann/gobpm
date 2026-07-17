package exec_test

import (
	"errors"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/exec"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/stretchr/testify/require"
)

// TestCallOutcome (SRD-050): the synthetic call completion carries the fault
// (nil on a clean completion) and rides as a flow.EventDefinition.
func TestCallOutcome(t *testing.T) {
	ok := exec.NewCallOutcome(nil)
	require.NoError(t, ok.Err())
	require.Nil(t, ok.GetItemsList())
	require.NotEmpty(t, ok.Type())

	var _ flow.EventDefinition = ok

	boom := errors.New("child faulted")
	require.ErrorIs(t, exec.NewCallOutcome(boom).Err(), boom)
}
