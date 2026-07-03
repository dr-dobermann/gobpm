package activities_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockflow"
	"github.com/dr-dobermann/gobpm/generated/mockrenv"
	"github.com/dr-dobermann/gobpm/pkg/interactor"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/hinteraction/consinp"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNewUserTask(t *testing.T) {
	t.Run("invalid parameters", func(t *testing.T) {
		// empty name
		_, err := activities.NewUserTask("")
		require.Error(t, err)

		// no output declared (a UserTask requires at least one)
		_, err = activities.NewUserTask("no output")
		require.Error(t, err)

		// nil renderer
		_, err = activities.NewUserTask("invalid",
			activities.WithRenderer(nil),
			activities.WithOutput("x", "string", true))
		require.Error(t, err)

		// duplicate renderer (same id)
		r, err := consinp.NewRenderer(
			consinp.WithMessager("Hello", "Hello"))
		require.NoError(t, err)
		_, err = activities.NewUserTask("invalid",
			activities.WithRenderer(r),
			activities.WithRenderer(r),
			activities.WithOutput("x", "string", true))
		require.Error(t, err)
	})

	t.Run("construction exposes renderers and outputs", func(t *testing.T) {
		r, err := consinp.NewRenderer(consinp.WithMessager("form", "form"))
		require.NoError(t, err)

		ut, err := activities.NewUserTask("Enter user info",
			activities.WithRenderer(r),
			activities.WithOutput("name", "string", true),
			activities.WithOutput("age", "int", true),
			activities.WithoutParams())
		require.NoError(t, err)

		require.Len(t, ut.Implementation(), 1)
		require.Contains(t, ut.Implementation(), consinp.ConsInpRender)
		require.Len(t, ut.Renderers(), 1)

		oo := ut.Outputs()
		require.Len(t, oo, 2)
		for _, o := range []struct{ name, oType string }{
			{"name", "string"},
			{"age", "int"},
		} {
			require.True(t, slices.ContainsFunc(oo,
				func(rp *bpmncommon.ResourceParameter) bool {
					return rp.Name() == o.name && rp.Type() == o.oType
				}), "missing output %s:%s", o.name, o.oType)
		}
	})
}

func TestUserTaskProcessEventAndExec(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	newUT := func(t *testing.T) *activities.UserTask {
		t.Helper()
		ut, err := activities.NewUserTask("ut",
			activities.WithOutput("name", "string", true),
			activities.WithoutParams())
		require.NoError(t, err)
		return ut
	}

	outputs := []data.Data{
		data.MustParameter("name",
			data.MustItemAwareElement(
				data.MustItemDefinition(values.NewVariable("John")),
				data.ReadyDataState)),
	}

	t.Run("completion binds outputs then advances", func(t *testing.T) {
		ut := newUT(t)

		comp := interactor.NewTaskCompletion(outputs)
		require.NoError(t, ut.ProcessEvent(t.Context(), comp))

		mrenv := mockrenv.NewMockRuntimeEnvironment(t)
		mrenv.EXPECT().Put(mock.Anything).Return(nil).Once()

		flows, err := ut.Exec(t.Context(), mrenv)
		require.NoError(t, err)
		require.Empty(t, flows) // standalone task — no outgoing flow
	})

	t.Run("no completion binds nothing", func(t *testing.T) {
		ut := newUT(t)

		// Put must NOT be called (no outputs stored).
		mrenv := mockrenv.NewMockRuntimeEnvironment(t)

		flows, err := ut.Exec(t.Context(), mrenv)
		require.NoError(t, err)
		require.Empty(t, flows)
	})

	t.Run("output binding failure fails Exec", func(t *testing.T) {
		ut := newUT(t)

		comp := interactor.NewTaskCompletion(outputs)
		require.NoError(t, ut.ProcessEvent(t.Context(), comp))

		mrenv := mockrenv.NewMockRuntimeEnvironment(t)
		mrenv.EXPECT().Put(mock.Anything).
			Return(errors.New("boom")).Once()

		_, err := ut.Exec(t.Context(), mrenv)
		require.Error(t, err)
	})

	t.Run("non-completion event rejected", func(t *testing.T) {
		ut := newUT(t)

		other := mockflow.NewMockEventDefinition(t)
		other.EXPECT().Type().Return(flow.TriggerSignal).Maybe()

		require.Error(t, ut.ProcessEvent(t.Context(), other))
	})
}
