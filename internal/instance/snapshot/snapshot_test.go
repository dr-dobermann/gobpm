package snapshot_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/stretchr/testify/require"
)

func TestSnapshot(t *testing.T) {
	p, err := process.New("test")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	task, err := activities.NewServiceTask(
		"task1",
		service.MustOperation("test_op", nil, nil, nil),
		activities.WithoutParams())
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")

	require.NoError(t, p.Add(start))
	require.NoError(t, p.Add((task)))
	require.NoError(t, p.Add(end))

	_, err = flow.Link(start, task)
	require.NoError(t, err)

	_, err = flow.Link(task, end)
	require.NoError(t, err)

	_, err = snapshot.New(p)
	require.NoError(t, err)
}
