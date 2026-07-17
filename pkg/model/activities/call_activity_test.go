package activities_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/exec"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/stretchr/testify/require"
)

func TestCallActivityModel(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	t.Run("construction and accessors", func(t *testing.T) {
		ca, err := activities.NewCallActivity("call", "billing")
		require.NoError(t, err)
		require.Equal(t, "billing", ca.CalledKey())
		require.Zero(t, ca.CalledVersion(), "default = latest-at-launch")
		require.Equal(t, flow.CallActivity, ca.ActivityType())
		require.Equal(t, flow.ActivityNodeType, ca.NodeType())
	})

	t.Run("empty and blank keys rejected", func(t *testing.T) {
		_, err := activities.NewCallActivity("call", "")
		require.Error(t, err)
		_, err = activities.NewCallActivity("call", "   ")
		require.Error(t, err)
	})

	t.Run("version pin", func(t *testing.T) {
		ca, err := activities.NewCallActivity("call", "billing",
			activities.WithCalledVersion(3))
		require.NoError(t, err)
		require.Equal(t, 3, ca.CalledVersion())

		_, err = activities.NewCallActivity("call", "billing",
			activities.WithCalledVersion(0))
		require.Error(t, err, "a pin is 1-based")
	})

	t.Run("invalid base option rejected", func(t *testing.T) {
		_, err := activities.NewCallActivity("call", "billing",
			options.WithName("not an activity option"))
		require.Error(t, err)
	})

	t.Run("Node returns the CallActivity itself", func(t *testing.T) {
		ca, err := activities.NewCallActivity("call", "billing")
		require.NoError(t, err)
		require.Same(t, ca, ca.Node())
	})

	t.Run("not a scope host", func(t *testing.T) {
		ca, err := activities.NewCallActivity("call", "billing")
		require.NoError(t, err)

		_, hasNodes := interface{}(ca).(interface{ Nodes() []flow.Node })
		require.False(t, hasNodes,
			"a CallActivity must not classify as a composite")
	})
}

func TestCallActivityDeclaredIO(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	param := func(name string) *data.Parameter {
		return data.MustParameter(name,
			data.MustItemAwareElement(
				data.MustItemDefinition(values.NewVariable(0),
					foundation.WithID(name)),
				data.ReadyDataState))
	}

	t.Run("declared input and output names", func(t *testing.T) {
		ca, err := activities.NewCallActivity("call", "billing",
			activities.WithParameters(data.Input, param("seed")),
			activities.WithParameters(data.Output, param("result")))
		require.NoError(t, err)

		require.Equal(t, []string{"seed"}, ca.CallInputs())
		require.Equal(t, []string{"result"}, ca.CallOutputs())
	})

	t.Run("empty IoSpec yields empty lists", func(t *testing.T) {
		ca, err := activities.NewCallActivity("call", "billing",
			activities.WithoutParams())
		require.NoError(t, err)

		require.Empty(t, ca.CallInputs())
		require.Empty(t, ca.CallOutputs())
	})

	t.Run("absent IoSpec yields empty lists", func(t *testing.T) {
		ca, err := activities.NewCallActivity("call", "billing")
		require.NoError(t, err)

		require.Empty(t, ca.CallInputs())
		require.Empty(t, ca.CallOutputs())
	})
}

func TestCallActivityValidate(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ca, err := activities.NewCallActivity("call", "billing")
	require.NoError(t, err)
	require.NoError(t, ca.Validate())
}

func TestCallActivityRuntimeSurface(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	t.Run("ProcessEvent accepts a call-outcome, rejects others", func(t *testing.T) {
		ca, err := activities.NewCallActivity("call", "billing")
		require.NoError(t, err)

		require.Error(t, ca.ProcessEvent(t.Context(), nil), "nil rejected")

		sig, err := events.NewSignal("cd",
			data.MustItemDefinition(values.NewVariable(1)))
		require.NoError(t, err)
		sdef, err := events.NewSignalEventDefinition(sig)
		require.NoError(t, err)
		require.Error(t, ca.ProcessEvent(t.Context(), sdef),
			"only a call-outcome resumes a Call Activity")

		require.NoError(t,
			ca.ProcessEvent(t.Context(), exec.NewCallOutcome(nil)))
	})

	t.Run("Exec selects the single outgoing on a clean completion", func(t *testing.T) {
		owner, err := activities.NewSubProcess("wrapper")
		require.NoError(t, err)

		ca, err := activities.NewCallActivity("call", "billing")
		require.NoError(t, err)
		next := spTask(t, "next-ca")

		require.NoError(t, owner.Add(ca))
		require.NoError(t, owner.Add(next))

		f, err := flow.Link(ca, next)
		require.NoError(t, err)

		require.NoError(t,
			ca.ProcessEvent(t.Context(), exec.NewCallOutcome(nil)))

		out, err := ca.Exec(t.Context(), nil)
		require.NoError(t, err)
		require.Len(t, out, 1)
		require.Equal(t, f.ID(), out[0].ID())
	})

	t.Run("Exec returns the child fault", func(t *testing.T) {
		ca, err := activities.NewCallActivity("call", "billing")
		require.NoError(t, err)

		boom := errs.New(errs.M("child faulted"))
		require.NoError(t,
			ca.ProcessEvent(t.Context(), exec.NewCallOutcome(boom)))

		_, err = ca.Exec(t.Context(), nil)
		require.ErrorIs(t, err, boom,
			"a child fault propagates so the caller track faults")
	})

	t.Run("clone failure propagates", func(t *testing.T) {
		ca, err := activities.NewCallActivity("bad-prop", "billing",
			data.WithProperties(&data.Property{}))
		require.NoError(t, err)

		_, err = ca.Clone()
		require.Error(t, err)
		require.Contains(t, err.Error(), "couldn't clone call activity")
	})

	t.Run("clone copies the binding, stays disjoint", func(t *testing.T) {
		ca, err := activities.NewCallActivity("call", "billing",
			activities.WithCalledVersion(2))
		require.NoError(t, err)

		cn, err := ca.Clone()
		require.NoError(t, err)

		cc, ok := cn.(*activities.CallActivity)
		require.True(t, ok)
		require.NotSame(t, ca, cc)
		require.Equal(t, "billing", cc.CalledKey())
		require.Equal(t, 2, cc.CalledVersion())
	})
}
