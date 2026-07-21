package activities_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/stretchr/testify/require"
)

// SRD-059 T-1 — ValidateCompensationPlacement through SubProcess.Validate: a
// compensation handler wired into normal flow is rejected; an unwired one
// passes and does not disturb the embedded entry shape (FR-2).

func placementTask(
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

func TestCompensationPlacementInSubProcess(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	// body: s-start → work → s-end, plus the compensation handler.
	build := func(t *testing.T, wireHandler bool) *activities.SubProcess {
		t.Helper()

		sp, err := activities.NewSubProcess("body")
		require.NoError(t, err)
		sStart, err := events.NewStartEvent("s-start")
		require.NoError(t, err)
		work := placementTask(t, "work", false)
		sEnd, err := events.NewEndEvent("s-end")
		require.NoError(t, err)
		undo := placementTask(t, "undo", true)

		for _, e := range []flow.Element{sStart, work, sEnd, undo} {
			require.NoError(t, sp.Add(e))
		}
		_, err = flow.Link(sStart, work)
		require.NoError(t, err)

		if wireHandler {
			// the invalid shape: the handler wired into normal flow.
			_, err = flow.Link(work, undo)
			require.NoError(t, err)
			_, err = flow.Link(undo, sEnd)
			require.NoError(t, err)
		} else {
			_, err = flow.Link(work, sEnd)
			require.NoError(t, err)
		}

		return sp
	}

	t.Run("flow-wired handler rejected", func(t *testing.T) {
		err := build(t, true).Validate()
		require.Error(t, err)
		require.ErrorContains(t, err, "must not carry normal sequence flow")
	})

	t.Run("unwired handler passes, shape intact", func(t *testing.T) {
		require.NoError(t, build(t, false).Validate())
	})
}
