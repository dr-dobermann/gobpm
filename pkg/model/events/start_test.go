package events_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/stretchr/testify/require"
)

func TestNewStartEvent(t *testing.T) {
	t.Run("empty definitions list",
		func(t *testing.T) {
			se, err := events.NewStartEvent("",
				"NoneTrigger", nil, nil, nil, true, true)

			require.NoError(t, err)
			require.NotNil(t, se)

			t.Log(se.Id())

			require.NotEqual(t, "", se.Id())
			require.Equal(t, "NoneTrigger", se.Name())
			require.Equal(t, 0, len(se.Triggers()))
			require.Equal(t, 0, len(se.Definitions(events.ShowDefinitionReferences)))
			require.Equal(t, 0, len(se.Definitions(events.ShowDefinitions)))
		})

	t.Run("empty definitions list with properties",
		func(t *testing.T) {
			se, err := events.NewStartEvent("",
				"NoneTrigger", []data.Property{
					*data.NewProperty("", "none_event_value",
						data.NewItemDefinition("", data.Information,
							nil, nil),
						nil),
				}, nil, nil, true, true)

			require.NoError(t, err)
			require.NotNil(t, se)

			t.Log(se.Id())

			props := se.Properties()

			require.Equal(t, 1, len(props))
			require.Equal(t, "none_event_value", props[0].Name())
		})

}
