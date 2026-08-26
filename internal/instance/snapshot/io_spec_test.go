package snapshot_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/stretchr/testify/require"
)

// TestSnapshotCarriesIOSpec — SRD-093 T-4: the process's declared contract
// rides the snapshot by reference — New sets it, Clone shares it — and a
// contract-less process leaves it nil.
func TestSnapshotCarriesIOSpec(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	in := data.MustParameter("subtotal",
		data.MustItemAwareElement(
			data.MustItemDefinition(values.NewVariable(0)),
			data.ReadyDataState))

	p, err := process.New("contracted", data.WithInputs(in))
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

	s, err := snapshot.New(p)
	require.NoError(t, err)
	require.Same(t, p.IOSpec(), s.IOSpec, "New carries the declaration")

	c, err := s.Clone()
	require.NoError(t, err)
	require.Same(t, s.IOSpec, c.IOSpec, "Clone shares it — a declaration")

	plain, err := process.New("plain")
	require.NoError(t, err)

	pStart, err := events.NewStartEvent("start")
	require.NoError(t, err)

	pEnd, err := events.NewEndEvent("end")
	require.NoError(t, err)

	require.NoError(t, plain.Add(pStart))
	require.NoError(t, plain.Add(pEnd))

	_, err = flow.Link(pStart, pEnd)
	require.NoError(t, err)

	ps, err := snapshot.New(plain)
	require.NoError(t, err)
	require.Nil(t, ps.IOSpec)
}
