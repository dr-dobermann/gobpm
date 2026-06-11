package activities_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockrenv"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/hinteraction/consinp"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/stretchr/testify/require"
)

type cloneExctr struct{}

func (cloneExctr) Type() string { return "clone executor" }

func (cloneExctr) ErrorClasses() []string { return nil }

func (cloneExctr) Execute(
	_ context.Context,
	in *data.ItemDefinition,
) (*data.ItemDefinition, error) {
	return in, nil
}

// TestServiceTaskClone verifies that ServiceTask.Clone shares configuration by
// reference, gives a per-instance operation, starts with empty flows and carries
// no container. The per-instance message-state isolation itself is covered by
// the service.Operation clone test.
func TestServiceTaskClone(t *testing.T) {
	data.CreateDefaultStates()

	in := bpmncommon.MustMessage("in_msg",
		data.MustItemDefinition(values.NewVariable(42)))
	out := bpmncommon.MustMessage("out_msg",
		data.MustItemDefinition(values.NewVariable(100)))

	op := service.MustOperation("op", in, out, cloneExctr{})

	st, err := activities.NewServiceTask("service", op,
		activities.WithoutParams())
	require.NoError(t, err)

	// outgoing flow on the original.
	ee, err := events.NewEndEvent("end")
	require.NoError(t, err)
	_, err = flow.Link(st, ee)
	require.NoError(t, err)

	clone, ok := st.Clone().(*activities.ServiceTask)
	require.True(t, ok)

	// independent object, same id, shared implementation description.
	require.NotSame(t, st, clone)
	require.Equal(t, st.ID(), clone.ID())
	require.Equal(t, st.Implementation(), clone.Implementation())

	// flows empty, no container.
	require.Empty(t, clone.Outgoing())
	require.Empty(t, clone.Incoming())
	require.Nil(t, clone.Container())
}

// TestUserTaskClone verifies that UserTask.Clone shares configuration by
// reference, starts with a fresh result channel, empty flows and no container.
func TestUserTaskClone(t *testing.T) {
	data.CreateDefaultStates()

	r, err := consinp.NewRenderer(
		consinp.WithMessager("hello", "hello"))
	require.NoError(t, err)

	ut, err := activities.NewUserTask("user",
		activities.WithRenderer(r),
		activities.WithOutput("name", "string", true),
		activities.WithoutParams())
	require.NoError(t, err)

	se, err := events.NewStartEvent("start")
	require.NoError(t, err)
	_, err = flow.Link(se, ut)
	require.NoError(t, err)

	clone, ok := ut.Clone().(*activities.UserTask)
	require.True(t, ok)

	// independent object, same id, shared renderers/outputs.
	require.NotSame(t, ut, clone)
	require.Equal(t, ut.ID(), clone.ID())
	require.Equal(t, ut.Renderers(), clone.Renderers())
	require.Equal(t, ut.Outputs(), clone.Outputs())

	// flows empty, no container.
	require.Empty(t, clone.Outgoing())
	require.Empty(t, clone.Incoming())
	require.Nil(t, clone.Container())

	// the clone is a working, independent node: Exec runs on it and reaches
	// interactor registration (it errors here only because the runtime provides
	// no RenderRegistrator), proving the clone carries no inherited exec state.
	mrenv := mockrenv.NewMockRuntimeEnvironment(t)
	mrenv.EXPECT().RenderRegistrator().Return(nil).Once()
	mrenv.EXPECT().InstanceID().Return("clone-test").Maybe()

	_, err = clone.Exec(context.Background(), mrenv)
	require.Error(t, err)
}
