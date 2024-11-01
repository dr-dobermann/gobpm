package activities_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
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
			a, err := activities.NewActivity("", activities.WithoutParams())

			require.Error(t, err)
			require.Empty(t, a)
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
			a, err := activities.NewActivity(
				"test activity",
				activities.WithCompensation(),
				activities.WithCompletionQuantity(5),
				activities.WithStartQuantity(2),
				activities.WithLoop(&activities.LoopCharacteristics{}),
				data.WithProperties(prop, prop),
				activities.WithRoles(rRole, rRole),
				foundation.WithId("test id"),
				activities.WithoutParams())

			require.NoError(t, err)
			require.NotEmpty(t, a)

			require.Equal(t, "test activity", a.Name())
			require.Equal(t, "test id", a.Id())

			rr := a.Roles()
			require.Equal(t, 1, len(rr))
			require.Equal(t, rRole.Name(), rr[0].Name())

			pp := a.Properties()
			require.Equal(t, 1, len(pp))
			require.Equal(t, prop.Name(), pp[0].Name())
			require.Equal(t, prop.Subject().Id(), pp[0].Subject().Id())
			require.Equal(t, 42, pp[0].Subject().Structure().Get())
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
			_, err = activities.NewActivity(
				"bad iospec",
				activities.WithSet("", "", data.Input, data.AllSets, []*data.Parameter{}),
				activities.WithSet("wrong dir set", "", "wrong direction", data.AllSets, []*data.Parameter{}),
				activities.WithSet("input set", "", data.Input, 42, []*data.Parameter{}),
			)
			require.Error(t, err)

			// invalid IOSpecs with no Sets
			a, err := activities.NewActivity(
				"iospec with no sets")
			require.Empty(t, a)
			require.Error(t, err)

			// normal IOSpecs with no parameters
			a, err = activities.NewActivity(
				"iospec without params",
				activities.WithSet("input set", "",
					data.Input, data.DefaultSet, nil),
				// duplicate set
				activities.WithSet("input set", "", data.Input, data.DefaultSet, nil),
				activities.WithSet("output_set", "", data.Output, data.DefaultSet, nil))
			require.NoError(t, err)
			require.NotEmpty(t, a)

			// full IOSpecs
			a, err = activities.NewActivity(
				"full iospec",
				activities.WithSet("input set", "", data.Input, data.DefaultSet, []*data.Parameter{pi}),
				activities.WithSet("output set", "", data.Output, data.DefaultSet, []*data.Parameter{po}),
			)
			require.NoError(t, err)
			require.NotEmpty(t, a)
		})
}
