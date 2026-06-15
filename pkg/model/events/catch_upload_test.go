package events_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/exec"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/require"
)

// TestCatchEventUploadData covers SRD-007 FR-6: the catch-side producer role
// is LIVE (its signature satisfies exec.NodeDataProducer — before the frame
// model it never matched and was dead code) and instantiates the event's
// outputs in the execution frame from the STATIC dataOutputs (the path for a
// payload-less trigger).
//
// The WS-C3 runtime-payload gap is closed (SRD-014): a catch event now
// captures a fired message payload via ProcessEvent (see
// catchevent_internal_test.go) and an IntermediateCatchEvent binds it into
// scope on resume. This test still asserts the static path, which remains the
// behaviour when no payload was captured.
func TestCatchEventUploadData(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	msg := bpmncommon.MustMessage(
		"greeting",
		data.MustItemDefinition(
			values.NewVariable("hi"),
			foundation.WithID("greeting_item")))

	med, err := events.NewMessageEventDefinition(msg, nil)
	require.NoError(t, err)

	se, err := events.NewStartEvent("msg-start",
		events.WithMessageTrigger(med))
	require.NoError(t, err)

	// the producer role matches (the pre-frame signature never did).
	var _ exec.NodeDataProducer = se

	pl, err := scope.New(scope.RootDataPath, nil)
	require.NoError(t, err)

	f, err := scope.NewFrame("track-1", se.ID(), pl.Root(), pl)
	require.NoError(t, err)

	require.NoError(t, se.UploadData(context.Background(), f))

	// the event's output instance is in the frame, carrying the message
	// item's value as a per-execution copy.
	outs := f.Outputs()
	require.Len(t, outs, 1)
	require.Equal(t, "greeting_item", outs[0].ItemDefinition().ID())
	require.Equal(t, "hi",
		outs[0].Value().Get(context.Background()))
}
