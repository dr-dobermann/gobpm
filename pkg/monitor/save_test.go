package monitor_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockmonitor"
	"github.com/dr-dobermann/gobpm/pkg/monitor"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestSave(t *testing.T) {
	w := mockmonitor.NewMockWriter(t)
	w.EXPECT().
		Write(mock.AnythingOfType("*monitor.Event")).
		RunAndReturn(
			func(evt *monitor.Event) {
				require.Equal(t, "save test", evt.Source)
				require.Equal(t, "info", evt.Type)
				require.Len(t, evt.Details, 1)
				require.Equal(t, "value", evt.Details["name"])
			})

	monitor.Save(w, "save test", "info", monitor.D("name", "value"))
}
