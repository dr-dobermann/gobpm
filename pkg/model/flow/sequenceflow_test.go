package flow_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/stretchr/testify/require"
)

func TestSequenceFlowEType(t *testing.T) {
	se, err := events.NewStartEvent("start")
	require.NoError(t, err)
	ee, err := events.NewEndEvent("end")
	require.NoError(t, err)

	sf, err := flow.Link(se, ee)
	require.NoError(t, err)
	require.Equal(t, flow.SequenceBaseElement, sf.EType())
}

// TestSequenceFlowContainerMismatch drives connect → checkConnections →
// SequenceFlow.Validate down its error branch: the source belongs to a process
// while the target belongs to none, so the endpoints are not in the same (or a
// uniformly nil) container. It also exercises getContainerID on both a non-nil
// container (the source's) and a nil one (the target's).
func TestSequenceFlowContainerMismatch(t *testing.T) {
	se, err := events.NewStartEvent("start")
	require.NoError(t, err)
	ee, err := events.NewEndEvent("end")
	require.NoError(t, err)

	proc, err := process.New("proc")
	require.NoError(t, err)

	// source bound to proc, target left unbound → container mismatch.
	require.NoError(t, proc.Add(se))

	_, err = flow.Link(se, ee)
	require.Error(t, err)
}

func TestSequenceFlow(t *testing.T) {
	se, err := events.NewStartEvent("start")
	require.NoError(t, err)

	ee, err := events.NewEndEvent("end")
	require.NoError(t, err)

	op, err := service.NewOperation("hello world!", nil, nil, nil)
	require.NoError(t, err)

	st1, err := activities.NewServiceTask("ServiceTask1",
		op, activities.WithoutParams())
	require.NoError(t, err)

	st2, err := activities.NewServiceTask("ServiceTask2",
		op, activities.WithoutParams())
	require.NoError(t, err)

	t.Run("invalid params",
		func(t *testing.T) {
			sf, err := flow.Link(nil, nil,
				options.WithName(""),
				flow.WithCondition(nil))
			require.Error(t, err)
			require.Empty(t, sf)

			_, err = flow.Link(se, nil)
			require.Error(t, err)

			_, err = flow.Link(se, st1,
				options.WithName(""),
				flow.WithCondition(nil))
			require.Error(t, err)

			_, err = flow.Link(se, st1, activities.WithStartQuantity(1))
			require.Error(t, err)

			// a failing base option propagates out of the flow's BaseElement build.
			_, err = flow.Link(se, st1, foundation.WithID("  "))
			require.Error(t, err)
		})

	t.Run("linked process",
		func(t *testing.T) {
			sfStart, err := flow.Link(se, st1,
				options.WithName("start"),
				foundation.WithID("start_link_id"),
				foundation.WithDoc("test", ""))
			require.NoError(t, err)
			require.Equal(t, se.ID(), sfStart.Source().ID())
			require.Equal(t, 0, len(sfStart.Source().Incoming()))
			require.Equal(t, 1, len(sfStart.Source().Outgoing()))
			require.Equal(t, st1.ID(), sfStart.Target().ID())

			sfST1, err := flow.Link(st1, st2, options.WithName("service task1"))
			require.NoError(t, err)
			require.NotEmpty(t, sfST1)

			sfST2, err := flow.Link(st2, ee, options.WithName("end flow"))
			require.NoError(t, err)
			require.NotEmpty(t, sfST2)

			require.Equal(t, 0, len(se.Incoming()))
			so := se.Outgoing()
			require.Equal(t, 1, len(so))
			require.Equal(t, "start", so[0].Name())
		})
}
