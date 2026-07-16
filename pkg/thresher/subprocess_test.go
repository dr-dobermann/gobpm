package thresher_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/stretchr/testify/require"
)

// SRD-049 e2e — the embedded Sub-Process through the public engine surface.

// readParent builds a task asserting it sees the parent property via the
// walk-up, and puts a child-scope local.
func readParent(t *testing.T, saw *atomic.Int32) *activities.ServiceTask {
	t.Helper()

	op, err := gooper.New("read-parent",
		func(ctx context.Context, ds service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			d, err := ds.GetData("order-id")
			if err != nil {
				return nil, err
			}

			if v, _ := d.Value().Get(ctx).(int); v == 77 {
				saw.Add(1)
			}

			return data.MustItemDefinition(
				values.NewVariable(1), foundation.WithID("scratch")), nil
		})
	require.NoError(t, err)

	st, err := activities.NewServiceTask("read-parent", op,
		activities.WithoutParams())
	require.NoError(t, err)

	return st
}

// TestEmbeddedSubProcessE2E (SRD-049 §6): a two-level nested body runs to
// completion — the inner task reads the parent's property through the
// walk-up, the scopes drain inside-out, and the parent continues.
func TestEmbeddedSubProcessE2E(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var saw, after atomic.Bool
	var sawParent atomic.Int32

	// inner: start → read-parent → end
	inner, err := activities.NewSubProcess("inner")
	require.NoError(t, err)

	iStart, err := events.NewStartEvent("i-start")
	require.NoError(t, err)
	rd := readParent(t, &sawParent)
	iEnd, err := events.NewEndEvent("i-end")
	require.NoError(t, err)

	for _, e := range []flow.Element{iStart, rd, iEnd} {
		require.NoError(t, inner.Add(e))
	}
	link(t, iStart, rd)
	link(t, rd, iEnd)

	// outer: start → mark → inner → end
	outer, err := activities.NewSubProcess("outer")
	require.NoError(t, err)

	oStart, err := events.NewStartEvent("o-start")
	require.NoError(t, err)
	mark := laneTask(t, "mark", &saw)
	oEnd, err := events.NewEndEvent("o-end")
	require.NoError(t, err)

	for _, e := range []flow.Element{oStart, mark, inner, oEnd} {
		require.NoError(t, outer.Add(e))
	}
	link(t, oStart, mark)
	link(t, mark, inner)
	link(t, inner, oEnd)

	// process: start → outer → tail → end, with the parent property.
	proc, err := process.New("embedded-e2e",
		data.WithProperties(
			data.MustProperty("order-id",
				data.MustItemDefinition(values.NewVariable(77),
					foundation.WithID("order-id")),
				data.ReadyDataState)))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	tail := laneTask(t, "tail", &after)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, outer, tail, end} {
		require.NoError(t, proc.Add(e))
	}
	link(t, start, outer)
	link(t, outer, tail)
	link(t, tail, end)

	require.NoError(t, runFlows(t, proc))

	require.True(t, saw.Load(), "the outer body must run")
	require.EqualValues(t, 1, sawParent.Load(),
		"the inner task must see the parent property via the walk-up")
	require.True(t, after.Load(), "the parent must continue after the drain")
}
