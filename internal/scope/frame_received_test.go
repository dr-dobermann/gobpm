package scope

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// TestFrameReceived pins the delivery-payload carrier (SRD-085 FR-1):
// the staged item reads back, nil clears, and a fresh frame carries
// none.
func TestFrameReceived(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	f := &Frame{}
	require.Nil(t, f.Received(), "a fresh frame carries no payload")

	item := data.MustItemDefinition(values.NewVariable("PAY-1"),
		foundation.WithID("pay"))

	f.SetReceived(item)
	require.Same(t, item, f.Received())

	f.SetReceived(nil)
	require.Nil(t, f.Received(), "nil clears the staged payload")
}
