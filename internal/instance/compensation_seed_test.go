package instance

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/stretchr/testify/require"
)

// SRD-059 T-1 (seeding side) — an isForCompensation activity is flow-less by
// design and must NOT be seeded as an entry track (FR-2; the Link-catch
// seeding precedent, SRD-057 §3.6).
func TestCompensationHandlerNotSeeded(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var worked, undone atomic.Int32

	p, err := process.New("comp-seed")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	work := hitTask(t, "work", &worked, "", 0)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	undoOp, err := gooper.New("undo-op",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			undone.Add(1)

			return nil, nil
		})
	require.NoError(t, err)
	undo, err := activities.NewServiceTask("undo", undoOp,
		activities.WithoutParams(), activities.WithCompensation())
	require.NoError(t, err)

	for _, e := range []flow.Element{start, work, end, undo} {
		require.NoError(t, p.Add(e))
	}
	linkAll(t, [2]flow.Element{start, work}, [2]flow.Element{work, end})

	inst := runInstance(t, p)

	require.Equal(t, Completed, inst.State())
	require.NoError(t, inst.LastErr())
	require.EqualValues(t, 1, worked.Load(), "the normal flow ran")
	require.EqualValues(t, 0, undone.Load(),
		"the compensation handler was not seeded and never ran")
}
