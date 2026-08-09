package instance

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// TestExecEnvReceivedItem pins the capability's three sources (SRD-085
// FR-1): the receiving track's capture wins, a transient frame's staged
// item is the fallback, and an environment with neither carries none.
func TestExecEnvReceivedItem(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	item := data.MustItemDefinition(values.NewVariable("PAY-2"),
		foundation.WithID("pay"))

	tr := &track{receivedItem: item}
	require.Same(t, item,
		newExecEnv(nil, nil, tr).ReceivedItem(),
		"the receiving track's capture wins")

	f := &scope.Frame{}
	f.SetReceived(item)
	require.Same(t, item, newExecEnv(nil, f, nil).ReceivedItem(),
		"a transient frame's staged item is the fallback")

	require.Nil(t, newExecEnv(nil, nil, nil).ReceivedItem(),
		"no track, no frame — no payload")
}
