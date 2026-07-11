package waiters_test

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/eventproc/eventhub/waiters"
	"github.com/dr-dobermann/gobpm/pkg/clock/clocktest"
	"github.com/dr-dobermann/gobpm/pkg/messaging"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// capHandler captures emitted slog records for level/message/attr assertions
// (FIX-022 §4.2 — the ADR-022 boundary logs must actually appear).
type capHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *capHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *capHandler) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h *capHandler) WithGroup(string) slog.Handler            { return h }

func (h *capHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())

	return nil
}

// find returns the first captured record at level whose message contains msg,
// and its "error" attribute value, or ok=false.
func (h *capHandler) find(level slog.Level, msg string) (errAttr string, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for _, r := range h.records {
		if r.Level != level || !strings.Contains(r.Message, msg) {
			continue
		}

		r.Attrs(func(a slog.Attr) bool {
			if a.Key == "error" {
				errAttr = a.Value.String()
			}

			return true
		})

		return errAttr, true
	}

	return "", false
}

// capRT is enginert.Default with a capturing logger.
func capRT() (*enginert.Runtime, *capHandler) {
	h := &capHandler{}

	return enginert.Default().WithLogger(slog.New(h)), h
}

// TestMessageWaiterJoinsHubReportOnDeliveryFailure (FIX-022 §4.1.1, A3+E1): a
// processor failure AND a failing WaiterFired both reach the goroutine-top Error
// record via errors.Join — neither is swallowed (ADR-022 §2.2/§2.3).
func TestMessageWaiterJoinsHubReportOnDeliveryFailure(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	rt, cap := capRT()
	eDef := msgEventDef(t)

	hub := mockeventproc.NewMockEventHub(t)
	hub.EXPECT().WaiterFired(eDef.ID()).
		Return(fmt.Errorf("hub report failed")).Maybe()

	released := make(chan struct{})
	ep := mockeventproc.NewMockEventProcessor(t)
	ep.EXPECT().ProcessEvent(mock.Anything, mock.Anything).
		RunAndReturn(func(context.Context, flow.EventDefinition) error {
			close(released)

			return fmt.Errorf("delivery boom")
		})

	w, err := waiters.NewMessageWaiter(hub, ep, eDef, "", rt)
	require.NoError(t, err)
	require.NoError(t, w.Service(context.Background()))

	require.NoError(t, rt.MessageBroker().Publish(context.Background(),
		messaging.Envelope{Name: "order placed", Payload: "x"}))

	<-released
	select {
	case <-w.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("waiter did not stop after a delivery failure")
	}

	errAttr, ok := cap.find(slog.LevelError, "message waiter terminally failed")
	require.True(t, ok, "the terminal fault must log an Error record")
	require.Contains(t, errAttr, "delivery boom", "the delivery error is reported")
	require.Contains(t, errAttr, "hub report failed",
		"the joined hub-report error is reported too (not swallowed)")
}

// TestMessageWaiterFailFastOnHubReportError (FIX-022 §4.1.1, A4): the SUCCESS
// path propagates a WaiterFired error (invariant-only failure = hub-state
// divergence, ADR-022 §2.3 fail-fast) — the waiter stops and logs Error, rather
// than swallowing the report as nil.
func TestMessageWaiterFailFastOnHubReportError(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	rt, cap := capRT()
	eDef := msgEventDef(t)

	hub := mockeventproc.NewMockEventHub(t)
	hub.EXPECT().WaiterFired(eDef.ID()).
		Return(fmt.Errorf("waiter isn't found")).Maybe()

	delivered := make(chan struct{})
	ep := mockeventproc.NewMockEventProcessor(t)
	ep.EXPECT().ProcessEvent(mock.Anything, mock.Anything).
		RunAndReturn(func(context.Context, flow.EventDefinition) error {
			close(delivered)

			return nil // delivery SUCCEEDS; only the hub report fails
		})

	w, err := waiters.NewMessageWaiter(hub, ep, eDef, "", rt)
	require.NoError(t, err)
	require.NoError(t, w.Service(context.Background()))

	require.NoError(t, rt.MessageBroker().Publish(context.Background(),
		messaging.Envelope{Name: "order placed", Payload: "x"}))

	<-delivered
	select {
	case <-w.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("waiter did not fail-fast on the hub-report error")
	}

	errAttr, ok := cap.find(slog.LevelError, "message waiter terminally failed")
	require.True(t, ok, "a hub-report failure on the success path must surface")
	require.Contains(t, errAttr, "waiter isn't found")
}

// TestTimerWaiterLogsRealFailureNotCompletion (FIX-022 §4.1.2, A5+E2): a real
// delivery failure logs an Error at the goroutine top; a normal one-shot
// completion (the errTimerCompleted sentinel) is silent.
func TestTimerWaiterLogsRealFailureNotCompletion(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	t.Run("normal completion is silent", func(t *testing.T) {
		clk := clocktest.New(time.Now())
		rt, cap := capRT()
		rt = rt.WithClock(clk)

		hub := mockeventproc.NewMockEventHub(t)
		hub.EXPECT().WaiterFired(mock.Anything).Return(nil).Maybe()

		fired := make(chan struct{}, 1)
		ep := mockeventproc.NewMockEventProcessor(t)
		ep.EXPECT().ProcessEvent(mock.Anything, mock.Anything).
			RunAndReturn(func(context.Context, flow.EventDefinition) error {
				fired <- struct{}{}

				return nil
			})

		w, err := waiters.NewTimeWaiter(hub, ep, oneShotTimerEDef(t), "", rt)
		require.NoError(t, err)
		require.NoError(t, w.Service(context.Background()))

		advanceUntilFire(t, clk, fired, time.Second)
		select {
		case <-w.Done():
		case <-time.After(2 * time.Second):
			t.Fatal("one-shot timer did not complete")
		}

		_, ok := cap.find(slog.LevelError, "timer waiter delivery failed")
		require.False(t, ok, "a normal completion must not log an Error")
	})

	t.Run("terminal hub-report failure logs Error", func(t *testing.T) {
		clk := clocktest.New(time.Now())
		rt, cap := capRT()
		rt = rt.WithClock(clk)

		// delivery succeeds, but the terminal WaiterFired reports hub-state
		// divergence — fail-fast (ADR-022 §2.3): propagate, so E2 logs Error.
		hub := mockeventproc.NewMockEventHub(t)
		hub.EXPECT().WaiterFired(mock.Anything).
			Return(fmt.Errorf("waiter isn't found")).Maybe()

		fired := make(chan struct{}, 1)
		ep := mockeventproc.NewMockEventProcessor(t)
		ep.EXPECT().ProcessEvent(mock.Anything, mock.Anything).
			RunAndReturn(func(context.Context, flow.EventDefinition) error {
				fired <- struct{}{}

				return nil
			})

		w, err := waiters.NewTimeWaiter(hub, ep, oneShotTimerEDef(t), "", rt)
		require.NoError(t, err)
		require.NoError(t, w.Service(context.Background()))

		advanceUntilFire(t, clk, fired, time.Second)
		select {
		case <-w.Done():
		case <-time.After(2 * time.Second):
			t.Fatal("timer did not stop after the terminal hub-report error")
		}

		errAttr, ok := cap.find(slog.LevelError, "timer waiter delivery failed")
		require.True(t, ok, "a terminal hub-report failure must log an Error")
		require.Contains(t, errAttr, "waiter isn't found")
	})

	t.Run("real delivery failure logs Error", func(t *testing.T) {
		clk := clocktest.New(time.Now())
		rt, cap := capRT()
		rt = rt.WithClock(clk)

		hub := mockeventproc.NewMockEventHub(t)
		hub.EXPECT().WaiterFired(mock.Anything).Return(nil).Maybe()

		fired := make(chan struct{}, 1)
		ep := mockeventproc.NewMockEventProcessor(t)
		ep.EXPECT().ProcessEvent(mock.Anything, mock.Anything).
			RunAndReturn(func(context.Context, flow.EventDefinition) error {
				fired <- struct{}{}

				return fmt.Errorf("timer delivery boom")
			})

		w, err := waiters.NewTimeWaiter(hub, ep, oneShotTimerEDef(t), "", rt)
		require.NoError(t, err)
		require.NoError(t, w.Service(context.Background()))

		advanceUntilFire(t, clk, fired, time.Second)
		select {
		case <-w.Done():
		case <-time.After(2 * time.Second):
			t.Fatal("timer did not stop after a delivery failure")
		}

		errAttr, ok := cap.find(slog.LevelError, "timer waiter delivery failed")
		require.True(t, ok, "a real delivery failure must log an Error")
		require.Contains(t, errAttr, "timer delivery boom")
	})
}

// oneShotTimerEDef builds a single-fire timeDate timer one second out.
func oneShotTimerEDef(t *testing.T) *events.TimerEventDefinition {
	t.Helper()

	return events.MustTimerEventDefinition(
		goexpr.Must(nil,
			data.MustItemDefinition(values.NewVariable(time.Now())),
			func(context.Context, data.Source) (data.Value, error) {
				return values.NewVariable(time.Now().Add(time.Second)), nil
			}),
		nil, nil)
}
