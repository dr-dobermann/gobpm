package flow_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/stretchr/testify/require"
)

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
			sf, err := flow.NewSequenceFlow(nil, nil,
				options.WithName(""),
				flow.WithCondition(nil))
			require.Error(t, err)
			require.Empty(t, sf)
		})

	t.Run("linked process",
		func(t *testing.T) {
			sfStart, err := se.Link(st1, options.WithName("start"))
			require.NoError(t, err)
			require.Equal(t, se.Id(), sfStart.Source().GetNode().Id())
			require.Equal(t, 0, len(sfStart.Source().GetNode().Incoming()))
			require.Equal(t, 1, len(sfStart.Source().GetNode().Outgoing()))
			require.Equal(t, st1.Id(), sfStart.Target().GetNode().Id())

			sfST1, err := st1.Link(st2, options.WithName("service task1"))
			require.NoError(t, err)
			require.NotEmpty(t, sfST1)

			sfST2, err := st2.Link(ee, options.WithName("end flow"))
			require.NoError(t, err)
			require.NotEmpty(t, sfST2)

			require.Equal(t, 0, len(se.Incoming()))
			so := se.Outgoing()
			require.Equal(t, 1, len(so))
			require.Equal(t, "start", so[0].Name())
		})
}
