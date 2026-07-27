package thresher

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// pinProcess builds the minimal registrable process.
func pinProcess(t *testing.T, id string) *process.Process {
	t.Helper()

	p, err := process.New(id, foundation.WithID(id))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, end} {
		require.NoError(t, p.Add(e))
	}

	_, err = flow.Link(start, end)
	require.NoError(t, err)

	return p
}

// TestSnapshotVersionPin covers SRD-070 T-5's stamp half: every
// registration stamps its minted version into the snapshot, and clones
// carry it — the checkpoint's (ProcessID, Version) attribution source.
func TestSnapshotVersionPin(t *testing.T) {
	th, err := New("test-version-pin", WithoutBanner())
	require.NoError(t, err)

	reg1, err := th.RegisterProcess(pinProcess(t, "pinned"))
	require.NoError(t, err)
	require.Equal(t, 1, reg1.Version())
	require.Equal(t, 1, reg1.snapshot.Version,
		"the snapshot must carry the minted version")

	reg2, err := th.RegisterProcess(pinProcess(t, "pinned"))
	require.NoError(t, err)
	require.Equal(t, 2, reg2.snapshot.Version)
	require.Equal(t, 1, reg1.snapshot.Version,
		"an earlier version's snapshot keeps its own pin")

	clone, err := reg2.snapshot.Clone()
	require.NoError(t, err)
	require.Equal(t, 2, clone.Version,
		"the per-instance clone inherits the pin")
}
