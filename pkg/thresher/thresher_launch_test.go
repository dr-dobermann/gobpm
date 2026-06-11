package thresher_test

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

// TestStartProcess_RunsToCompletion guards launchInstance against prematurely
// cancelling the instance context: inst.Run is non-blocking, so deferring the
// cancel in launchInstance tore the instance down the moment it returned and no
// plain (non-event-triggered) process ever executed a node. The cancel must be
// retained for later teardown, not deferred.
//
// The service task's operation signals a channel from inside Exec; if the
// instance is cancelled before it runs (the bug), the signal never arrives.
func TestStartProcess_RunsToCompletion(t *testing.T) {
	th, err := thresher.New("run-to-completion")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, th.Run(ctx))

	ran := make(chan struct{}, 1)

	impl, err := gooper.New(
		func(_ context.Context, _ *data.ItemDefinition) (*data.ItemDefinition, error) {
			ran <- struct{}{}

			return nil, nil
		})
	require.NoError(t, err)

	op, err := service.NewOperation("work-op", nil, nil, impl)
	require.NoError(t, err)

	// start -> work -> end
	proc, err := process.New("runnable")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	work, err := activities.NewServiceTask("work", op, activities.WithoutParams())
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, work, end} {
		require.NoError(t, proc.Add(e))
	}

	_, err = flow.Link(start, work)
	require.NoError(t, err)
	_, err = flow.Link(work, end)
	require.NoError(t, err)

	require.NoError(t, th.RegisterProcess(proc))
	require.NoError(t, th.StartProcess(proc.ID()))

	select {
	case <-ran:
		// the node executed — the instance was not torn down on launch.
	case <-time.After(3 * time.Second):
		t.Fatal("process did not execute its node: launchInstance cancelled " +
			"the instance prematurely")
	}
}
