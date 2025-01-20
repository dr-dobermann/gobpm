package activities

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
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

			// empty roles
			_, err = newActivity("invalid roles", WithRoles(nil))
			require.Error(t, err)

			// empty properties
			_, err = newActivity("invalid roles", data.WithProperties(nil))
			require.Error(t, err)
		})

	prop, err := data.NewProperty(
		"test property",
		data.MustItemDefinition(values.NewVariable(42)),
		data.MustDataState("ready"))
	require.NoError(t, err)

	rRole, err := hi.NewResourceRole("specialist", nil, nil, nil)
	require.NoError(t, err)

	t.Run("full options without parameters",
		func(t *testing.T) {
			a, err := newActivity(
				"test activity",
				WithCompensation(),
				WithCompletionQuantity(5),
				WithStartQuantity(2),
				WithLoop(&LoopCharacteristics{}),
				data.WithProperties(prop, prop),
				WithRoles(rRole, rRole),
				foundation.WithId("test id"),
				WithoutParams())

			require.NoError(t, err)
			require.NotEmpty(t, a)

			require.Equal(t, a, a.Node())

			require.Equal(t, "test activity", a.Name())
			require.Equal(t, "test id", a.Id())

			rr := a.Roles()
			require.Equal(t, 1, len(rr))
			require.Equal(t, rRole.Name(), rr[0].Name())

			pp := a.Properties()
			require.Equal(t, 1, len(pp))
			require.Equal(t, prop.Name(), pp[0].Name())
			require.Equal(t, prop.Subject().Id(), pp[0].Subject().Id())
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
			is, err := data.NewDataState("initial_state")
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

			// invlaid sets
			_, err = newActivity(
				"bad iospec",
				WithSet("", "", data.Input, data.AllSets, []*data.Parameter{}),
				WithSet("wrong dir set", "", "wrong direction", data.AllSets, []*data.Parameter{}),
				WithSet("input set", "", data.Input, 42, []*data.Parameter{}),
			)
			require.Error(t, err)

			// invalid IOSpecs with no Sets
			a, err := newActivity(
				"iospec with no sets")
			require.Empty(t, a)
			require.Error(t, err)

			// invalid IOSpecs with duplicate empty sets
			_, err = newActivity(
				"iospec without params",
				// duplicate set
				WithEmptySet("input set", "", data.Input),
				WithEmptySet("output set", "", data.Output),
				WithEmptySet("duplicate output set", "", data.Output),
			)
			require.Error(t, err)

			// normal IOSpecs with no parameters
			a, err = newActivity(
				"iospec without params",
				WithEmptySet("input set", "", data.Input),
				WithEmptySet("output_set", "", data.Output),
			)
			require.NoError(t, err)
			require.NotEmpty(t, a)

			// full IOSpecs
			a, err = newActivity(
				"full iospec",
				WithSet("input set", "", data.Input, data.DefaultSet, []*data.Parameter{pi}),
				WithSet("output set", "", data.Output, data.DefaultSet, []*data.Parameter{po}),
			)
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

			require.NoError(t, a1.SetDefaultFlow(sf.Id()))
		})
}
