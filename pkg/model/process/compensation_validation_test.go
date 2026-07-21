package process_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/stretchr/testify/require"
)

// SRD-059 T-1 (container side) — an isForCompensation activity must not carry
// normal sequence flow; validated fail-fast at registration (FR-2), in both
// containers.

func compValTask(
	t *testing.T, name string, marked bool,
) *activities.ServiceTask {
	t.Helper()

	opts := []options.Option{activities.WithoutParams()}
	if marked {
		opts = append(opts, activities.WithCompensation())
	}

	st, err := activities.NewServiceTask(name,
		service.MustOperation(name+"-op", nil, nil, nil), opts...)
	require.NoError(t, err)

	return st
}

// TestCompensationPlacementValidation: a flow-wired compensation activity is
// rejected by Process.Validate; an unwired one passes; the same rule holds
// inside a Sub-Process (whose embedded shape must also NOT count the handler
// as a flow-less entry).
func TestCompensationPlacementValidation(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	t.Run("flow-wired compensation activity rejected", func(t *testing.T) {
		p, err := process.New("comp-val-bad")
		require.NoError(t, err)

		start, err := events.NewStartEvent("start")
		require.NoError(t, err)
		comp := compValTask(t, "undo", true)
		end, err := events.NewEndEvent("end")
		require.NoError(t, err)

		for _, e := range []flow.Element{start, comp, end} {
			require.NoError(t, p.Add(e))
		}
		_, err = flow.Link(start, comp)
		require.NoError(t, err)
		_, err = flow.Link(comp, end)
		require.NoError(t, err)

		err = p.Validate()
		require.Error(t, err)
		require.ErrorContains(t, err, "must not carry normal sequence flow")
	})

	t.Run("unwired handler passes; sub-process shape unaffected", func(t *testing.T) {
		// sub-process: s-start → work → s-end, PLUS an unwired compensation
		// handler — the handler must not count as a flow-less entry, so the
		// unique-None-start embedded shape stays valid.
		sp, err := activities.NewSubProcess("body")
		require.NoError(t, err)
		sStart, err := events.NewStartEvent("s-start")
		require.NoError(t, err)
		work := compValTask(t, "work", false)
		sEnd, err := events.NewEndEvent("s-end")
		require.NoError(t, err)
		undo := compValTask(t, "undo", true)

		for _, e := range []flow.Element{sStart, work, sEnd, undo} {
			require.NoError(t, sp.Add(e))
		}
		_, err = flow.Link(sStart, work)
		require.NoError(t, err)
		_, err = flow.Link(work, sEnd)
		require.NoError(t, err)

		p, err := process.New("comp-val-ok")
		require.NoError(t, err)
		start, err := events.NewStartEvent("start")
		require.NoError(t, err)
		end, err := events.NewEndEvent("end")
		require.NoError(t, err)
		for _, e := range []flow.Element{start, sp, end} {
			require.NoError(t, p.Add(e))
		}
		_, err = flow.Link(start, sp)
		require.NoError(t, err)
		_, err = flow.Link(sp, end)
		require.NoError(t, err)

		require.NoError(t, p.Validate())
	})
}
