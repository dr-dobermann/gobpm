package activities_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/require"
)

func TestActivity(t *testing.T) {
	t.Run("empty params",
		func(t *testing.T) {
			a, err := activities.NewActivity("")

			require.Error(t, err)
			require.Empty(t, a)
		})

	prop, err := data.NewProperty(
		"test property",
		data.MustItemDefinition(values.NewVariable(42)),
		data.MustDataState("ready"))
	require.NoError(t, err)

	rRole, err := activities.NewResourceRole("specialist", nil, nil, nil)
	require.NoError(t, err)

	t.Run("full options",
		func(t *testing.T) {
			a, err := activities.NewActivity(
				"test activity",
				activities.WithCompensation(),
				activities.WithCompletionQuantity(5),
				activities.WithStartQuantity(2),
				activities.WithLoop(&activities.LoopCharacteristics{}),
				activities.WithProperties(prop, prop),
				activities.WithResources(rRole, rRole),
				foundation.WithId("test id"))

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

			require.Equal(t, flow.ActivityNode, a.NodeType())

			require.NoError(t, a.AcceptIncomingFlow(nil))
			require.NoError(t, a.ProvideOutgoingFlow(nil))

			require.NoError(t, a.SetDefaultFlow(""))
			require.Error(t, a.SetDefaultFlow("wrong_flow"))
		})
}
