package waiters_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/internal/eventproc/eventhub/waiters"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func signalEDef(t *testing.T, name string) *events.SignalEventDefinition {
	t.Helper()

	sig, err := events.NewSignal(name, nil)
	require.NoError(t, err)

	def, err := events.NewSignalEventDefinition(sig)
	require.NoError(t, err)

	return def
}

func TestNewSignalWaiterErrors(t *testing.T) {
	hub := mockeventproc.NewMockEventHub(t)
	ep := mockeventproc.NewMockEventProcessor(t)

	// empty dependencies
	_, err := waiters.NewSignalWaiter(nil, nil, nil, "", nil)
	require.Error(t, err)

	// a non-signal definition is rejected
	_, err = waiters.NewSignalWaiter(hub, ep, msgEventDef(t), "", enginert.Default())
	require.Error(t, err)
}

func TestSignalWaiterPassive(t *testing.T) {
	hub := mockeventproc.NewMockEventHub(t)
	ep := mockeventproc.NewMockEventProcessor(t)
	ep.EXPECT().ID().Return("ep-1").Maybe()

	def := signalEDef(t, "GO")
	w, err := waiters.NewSignalWaiter(hub, ep, def, "", enginert.Default())
	require.NoError(t, err)

	require.NotEmpty(t, w.ID())
	require.Equal(t, def, w.EventDefinition())

	require.NoError(t, w.Service(context.Background()))
	require.Equal(t, eventproc.WSRunned, w.State())

	// A passive waiter has no service goroutine: Done is closed at once.
	select {
	case <-w.Done():
	case <-time.After(time.Second):
		t.Fatal("signal waiter Done is not closed for a passive waiter")
	}

	require.NoError(t, w.Stop())
	require.Equal(t, eventproc.WSStopped, w.State())
}

func TestSignalWaiterBroadcastFanOut(t *testing.T) {
	hub := mockeventproc.NewMockEventHub(t)
	def := signalEDef(t, "GO")

	ep1 := mockeventproc.NewMockEventProcessor(t)
	ep1.EXPECT().ID().Return("ep-1").Maybe()
	ep1.EXPECT().ProcessEvent(context.Background(), def).Return(nil).Once()

	ep2 := mockeventproc.NewMockEventProcessor(t)
	ep2.EXPECT().ID().Return("ep-2").Maybe()
	ep2.EXPECT().ProcessEvent(context.Background(), def).Return(nil).Once()

	w, err := waiters.NewSignalWaiter(hub, ep1, def, "", enginert.Default())
	require.NoError(t, err)
	require.NoError(t, w.Service(context.Background()))
	require.NoError(t, w.AddEventProcessor(ep2))
	require.Len(t, w.EventProcessors(), 2)

	// Process fans the signal out to every registered catcher (broadcast).
	require.NoError(t, w.Process(def))

	// RemoveEventProcessor drops a catcher (a consumed/cancelled track).
	require.NoError(t, w.RemoveEventProcessor(ep1))
	require.Len(t, w.EventProcessors(), 1)
}

func TestSignalWaiterEdgeCases(t *testing.T) {
	hub := mockeventproc.NewMockEventHub(t)
	def := signalEDef(t, "GO")

	ep := mockeventproc.NewMockEventProcessor(t)
	ep.EXPECT().ID().Return("ep-1").Maybe()

	w, err := waiters.NewSignalWaiter(hub, ep, def, "", enginert.Default())
	require.NoError(t, err)

	// nil add/remove and removing an unregistered processor are errors.
	require.Error(t, w.AddEventProcessor(nil))
	require.Error(t, w.RemoveEventProcessor(nil))

	other := mockeventproc.NewMockEventProcessor(t)
	other.EXPECT().ID().Return("other").Maybe()
	require.Error(t, w.RemoveEventProcessor(other))

	// Service twice → not ready.
	require.NoError(t, w.Service(context.Background()))
	require.Error(t, w.Service(context.Background()))

	// Process tolerates a failing catcher: it logs and still returns nil so the
	// broadcast reaches the others (FR-3).
	failEP := mockeventproc.NewMockEventProcessor(t)
	failEP.EXPECT().ID().Return("fail").Maybe()
	failEP.EXPECT().ProcessEvent(mock.Anything, def).
		Return(errors.New("boom")).Once()
	require.NoError(t, w.AddEventProcessor(failEP))

	// A catcher that rejects the broadcast (ErrRejected — already fired / not
	// waiting, FIX-007) is a benign Debug no-op: Process skips it without a WARN
	// and still delivers to the rest.
	rejEP := mockeventproc.NewMockEventProcessor(t)
	rejEP.EXPECT().ID().Return("rej").Maybe()
	rejEP.EXPECT().ProcessEvent(mock.Anything, def).
		Return(eventproc.ErrRejected).Once()
	require.NoError(t, w.AddEventProcessor(rejEP))

	ep.EXPECT().ProcessEvent(mock.Anything, def).Return(nil).Once()
	require.NoError(t, w.Process(def))
}

// TestSignalWaiterProcessorCount (FIX-021): the broadcast-set size follows the
// processor list — one at construction, up on AddEventProcessor, down on
// RemoveEventProcessor. The hub's SignalCatchers probe reads it structurally.
func TestSignalWaiterProcessorCount(t *testing.T) {
	epA := mockeventproc.NewMockEventProcessor(t)
	epA.EXPECT().ID().Return("catcher-A").Maybe()
	epB := mockeventproc.NewMockEventProcessor(t)
	epB.EXPECT().ID().Return("catcher-B").Maybe()

	w, err := waiters.NewSignalWaiter(
		mockeventproc.NewMockEventHub(t), epA, signalEDef(t, "GO"), "",
		enginert.Default())
	require.NoError(t, err)

	pc, ok := w.(interface{ ProcessorCount() int })
	require.True(t, ok, "the signal waiter must expose ProcessorCount")

	require.Equal(t, 1, pc.ProcessorCount(), "the constructing catcher counts")

	require.NoError(t, w.AddEventProcessor(epB))
	require.Equal(t, 2, pc.ProcessorCount())

	require.NoError(t, w.RemoveEventProcessor(epA))
	require.Equal(t, 1, pc.ProcessorCount())
}
