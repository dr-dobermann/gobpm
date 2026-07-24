package events

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
)

// TestMsgOutputErr covers FIX-026: the payload-output failure classifier
// carries the message name.
func TestMsgOutputErr(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	msg, err := bpmncommon.NewMessage("order placed",
		data.MustItemDefinition(values.NewVariable("")))
	require.NoError(t, err)

	med, err := NewMessageEventDefinition(msg, nil)
	require.NoError(t, err)

	e := msgOutputErr(med, errs.New(errs.M("inner")))
	require.Contains(t, e.Error(), "order placed")
	require.Contains(t, e.Error(), "payload output")
}
