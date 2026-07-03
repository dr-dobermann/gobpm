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

	cn, err := st.Clone()
	require.NoError(t, err)

	clone, ok := cn.(*activities.ServiceTask)
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

	cn, err := ut.Clone()
	require.NoError(t, err)

	clone, ok := cn.(*activities.UserTask)
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

	// the clone is a working, independent node: Exec runs on it — with no
	// completion delivered it binds nothing and returns no outgoing flow — proving
	// the clone carries no inherited exec state.
	mrenv := mockrenv.NewMockRuntimeEnvironment(t)

	flows, err := clone.Exec(context.Background(), mrenv)
	require.NoError(t, err)
	require.Empty(t, flows)
}

// cloneOp builds a minimal service operation for a task under test.
func cloneOp(t *testing.T) service.Operation {
	t.Helper()

	return service.MustOperation("op",
		bpmncommon.MustMessage("in", data.MustItemDefinition(values.NewVariable(1))),
		bpmncommon.MustMessage("out", data.MustItemDefinition(values.NewVariable(2))),
		cloneExctr{})
}

// TestActivityCloneIsolatesProperties covers FIX-017 3.2.2: activity.clone
// deep-copies its properties, so a task clone owns distinct Property objects and
// a write through the source doesn't reach the clone.
func TestActivityCloneIsolatesProperties(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	prop, err := data.NewProperty(
		"counter",
		data.MustItemDefinition(values.NewVariable(0)),
		data.ReadyDataState)
	require.NoError(t, err)

	st, err := activities.NewServiceTask("service", cloneOp(t),
		data.WithProperties(prop), activities.WithoutParams())
	require.NoError(t, err)

	cn, err := st.Clone()
	require.NoError(t, err)

	clone, ok := cn.(*activities.ServiceTask)
	require.True(t, ok)

	require.NotSame(t, st.Properties()[0], clone.Properties()[0],
		"clone must own a distinct property object")

	ctx := context.Background()
	require.NoError(t, st.Properties()[0].Value().Update(ctx, 7))
	require.Equal(t, 0, clone.Properties()[0].Value().Get(ctx),
		"a property write on the source must not leak into the clone")
}

// TestUserTaskAcceptsProperty covers FIX-018 3.2.1: NewUserTask now accepts
// data.WithProperties and exposes the property.
func TestUserTaskAcceptsProperty(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	prop := data.MustProperty("counter",
		data.MustItemDefinition(values.NewVariable(0)), data.ReadyDataState)

	r, err := consinp.NewRenderer(consinp.WithMessager("hello", "hello"))
	require.NoError(t, err)

	ut, err := activities.NewUserTask("user", activities.WithRenderer(r),
		activities.WithOutput("name", "string", true),
		activities.WithoutParams(), data.WithProperties(prop))
	require.NoError(t, err)

	require.Len(t, ut.Properties(), 1)
	require.Equal(t, "counter", ut.Properties()[0].Name())
}
