package process_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// TestProcessRejectsTopLevelCancelEnd proves Process.Validate rejects a Cancel End
// Event at the top level — Cancel is valid only inside a Transaction Sub-Process
// (BPMN §10.7, ADR-028 §2.6).
func TestProcessRejectsTopLevelCancelEnd(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New("top-cancel")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	ced, err := events.NewCancelEventDefinition()
	require.NoError(t, err)
	cend, err := events.NewEndEvent("cx", events.WithCancelTrigger(ced))
	require.NoError(t, err)

	for _, e := range []flow.Element{start, cend} {
		require.NoError(t, p.Add(e))
	}

	_, err = flow.Link(start, cend)
	require.NoError(t, err)

	require.Error(t, p.Validate())
}
