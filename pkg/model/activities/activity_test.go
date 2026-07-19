package activities

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	hi "github.com/dr-dobermann/gobpm/pkg/model/hinteraction"
	"github.com/stretchr/testify/require"
)

func TestActivity(t *testing.T) {
	t.Run("empty params",
		func(t *testing.T) {
			// empty name
			a, err := newActivity("", WithoutParams())

			require.Error(t, err)
			require.Empty(t, a)

			// nil roles / properties are silently skipped (variadic
			// tolerance), not an error
			_, err = newActivity("nil roles skipped", WithRoles(nil))
			require.NoError(t, err)

			_, err = newActivity("nil props skipped", data.WithProperties(nil))
			require.NoError(t, err)
		})

	prop, err := data.NewProperty(
		"test property",
		data.MustItemDefinition(values.NewVariable(42)),
		data.MustSrcState("ready"))
	require.NoError(t, err)

	rRole, err := hi.NewResourceRole("specialist", nil, nil, nil)
	require.NoError(t, err)

	t.Run("full options without parameters",
		func(t *testing.T) {
			stdLoop, err := NewStandardLoop(
				goexpr.Must(nil,
					data.MustItemDefinition(values.NewVariable(true)),
					func(_ context.Context, _ data.Source) (data.Value, error) {
						return values.NewVariable(true), nil
					}))
			require.NoError(t, err)

			a, err := newActivity(
				"test activity",
				WithCompensation(),
				WithCompletionQuantity(5),
				WithStartQuantity(2),
				WithLoop(stdLoop),
				data.WithProperties(prop, prop),
				WithRoles(rRole, rRole),
				foundation.WithID("test id"),
				WithoutParams())

			require.NoError(t, err)
			require.NotEmpty(t, a)

			require.Equal(t, a, a.Node())

			require.Equal(t, "test activity", a.Name())
			require.Equal(t, "test id", a.ID())

			rr := a.Roles()
			require.Equal(t, 1, len(rr))
			require.Equal(t, rRole.Name(), rr[0].Name())

			pp := a.Properties()
			require.Equal(t, 1, len(pp))
			require.Equal(t, prop.Name(), pp[0].Name())
			require.Equal(t, prop.Subject().ID(), pp[0].Subject().ID())
			require.Equal(t, 42, pp[0].Subject().Structure().Get(context.Background()))
			require.Equal(t, "ready", pp[0].State().Name())

			require.Equal(t, flow.ActivityNodeType, a.NodeType())

			require.NoError(t, a.AcceptIncomingFlow(nil))
			require.NoError(t, a.SupportOutgoingFlow(nil))

			require.NoError(t, a.SetDefaultFlow(""))
			require.Error(t, a.SetDefaultFlow("wrong_flow"))
			require.Empty(t, a.BoundaryEvents())
		})

	t.Run("IOSpec test",
		func(t *testing.T) {
			is, err := data.NewSrcState("initial_state")
			require.NoError(t, err)

			paramItem, err := data.NewItemDefinition(
				values.NewVariable(42))
			require.NoError(t, err)

			pi, err := data.NewParameter("input_param",
				data.MustItemAwareElement(paramItem, is))
			require.NoError(t, err)

			po, err := data.NewParameter("output_param",
				data.MustItemAwareElement(paramItem, is))
			require.NoError(t, err)

			// invalid: WithParameters with a bad direction, or a nil parameter
			_, err = newActivity(
				"bad direction",
				WithParameters("wrong direction", pi))
			require.Error(t, err)

			_, err = newActivity(
				"nil param",
				WithParameters(data.Input, nil))
			require.Error(t, err)

			// an activity with no parameters declared is valid: empty I/O
			a, err := newActivity("no params")
			require.NoError(t, err)
			require.NotEmpty(t, a)

			// WithoutParams is equally valid
			a, err = newActivity("without params", WithoutParams())
			require.NoError(t, err)
			require.NotEmpty(t, a)

			// duplicate parameter name in one direction fails structural Validate
			_, err = newActivity(
				"dup names",
				WithParameters(data.Input,
					data.MustParameter("dup",
						data.MustItemAwareElement(paramItem, is)),
					data.MustParameter("dup",
						data.MustItemAwareElement(paramItem, is))))
			require.Error(t, err)

			// full IOSpec with an input and an output parameter
			a, err = newActivity(
				"full iospec",
				WithParameters(data.Input, pi),
				WithParameters(data.Output, po))
			require.NoError(t, err)
			require.NotEmpty(t, a)
		})

	t.Run("set default flow",
		func(t *testing.T) {
			a1, err := newActivity("act1", WithoutParams())
			require.NoError(t, err)

			a2, err := newActivity("act2", WithoutParams())
			require.NoError(t, err)

			sf, err := flow.Link(a1, a2)
			require.NoError(t, err)

			require.NoError(t, a1.SetDefaultFlow(sf.ID()))
		})
}
