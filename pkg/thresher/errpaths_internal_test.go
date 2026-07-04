package thresher

import (
	"context"
	"errors"
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestThresherPropagateEventHubError covers Thresher.PropagateEvent's
// hub-failure branch: a Started thresher forwards to its EventHub, and a hub
// error is wrapped and returned.
func TestThresherPropagateEventHubError(t *testing.T) {
	th, err := New("propagate-hub-error")
	require.NoError(t, err)

	hub := mockeventproc.NewMockEventHub(t)
	hub.EXPECT().PropagateEvent(mock.Anything, mock.Anything).
		Return(errors.New("hub boom"))
	th.eventHub = hub
	th.state.Store(uint32(Started))

	eDef, err := events.NewTerminateEventDefinition()
	require.NoError(t, err)

	require.Error(t, th.PropagateEvent(context.Background(), eDef))
}
