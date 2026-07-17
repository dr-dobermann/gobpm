package snapshot_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/stretchr/testify/require"
)

// TestEventSubStartNotInstantiator (SRD-052 FR-4): a top-level Event
// Sub-Process with a Message start does NOT create a process-instantiating
// start — its triggered start is inside the handler container, not a
// top-level node, so the snapshot's instantiating-start scan never sees it.
func TestEventSubStartNotInstantiator(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	// a normal process: None start → task → end.
	p, err := process.New("host")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	op, err := gooper.New("noop",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			return nil, nil
		})
	require.NoError(t, err)
	task, err := activities.NewServiceTask("task", op, activities.WithoutParams())
	require.NoError(t, err)

	// a top-level Event Sub-Process armed by a Message.
	es := messageEventSub(t)

	for _, e := range []flow.Element{start, task, end, es} {
		require.NoError(t, p.Add(e))
	}
	_, err = flow.Link(start, task)
	require.NoError(t, err)
	_, err = flow.Link(task, end)
	require.NoError(t, err)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	require.Empty(t, s.InstantiatingStarts,
		"the event sub-process's message start must not be a process instantiator")
}

// messageEventSub builds an Event Sub-Process: [message start] → task → end.
func messageEventSub(t *testing.T) *activities.SubProcess {
	t.Helper()

	es, err := activities.NewSubProcess("handler",
		activities.WithTriggeredByEvent())
	require.NoError(t, err)

	start, err := events.NewStartEvent("h-start",
		events.WithMessageTrigger(msgEDef(t, "ping")),
		events.WithInterrupting())
	require.NoError(t, err)

	op, err := gooper.New("h-op",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			return nil, nil
		})
	require.NoError(t, err)
	task, err := activities.NewServiceTask("h-task", op,
		activities.WithoutParams())
	require.NoError(t, err)

	end, err := events.NewEndEvent("h-end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, task, end} {
		require.NoError(t, es.Add(e))
	}
	_, err = flow.Link(start, task)
	require.NoError(t, err)
	_, err = flow.Link(task, end)
	require.NoError(t, err)

	return es
}
