package waiters_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/internal/eventproc/eventhub/waiters"
	"github.com/dr-dobermann/gobpm/pkg/clock/clocktest"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestTimeWaiter(t *testing.T) {
	t.Run("errors",
		func(t *testing.T) {
			ep := mockeventproc.NewMockEventProcessor(t)
			mockHub := mockeventproc.NewMockEventHub(t)

			ep.EXPECT().
				ProcessEvent(mock.Anything, mock.Anything).
				RunAndReturn(
					func(context.Context, flow.EventDefinition) error {
						return fmt.Errorf("event processing error")
					}).Maybe()

			// empty parameters
			_, err := waiters.NewTimeWaiter(nil, nil, nil, "", enginert.Default())
			require.Error(t, err)

			// invalid event definition
			_, err = waiters.NewTimeWaiter(mockHub, ep,
				events.MustSignalEventDefinition(
					&events.Signal{}), "", enginert.Default())
			require.Error(t, err)

			// failing evaluation
			failiEDef := events.MustTimerEventDefinition(
				goexpr.Must(
					nil,
					data.MustItemDefinition(
						values.NewVariable(time.Now())),
					func(_ context.Context, ds data.Source) (data.Value, error) {
						return nil, fmt.Errorf("failing evaluation")
					}),
				nil, nil)

			_, err = waiters.NewTimeWaiter(mockHub, ep, failiEDef, "", enginert.Default())
			require.Error(t, err)

			// past time
			pastEDef := events.MustTimerEventDefinition(
				goexpr.Must(
					nil,
					data.MustItemDefinition(
						values.NewVariable(time.Now())),
					func(_ context.Context, ds data.Source) (data.Value, error) {
						return values.NewVariable(time.Date(1917, time.October, 25, 21, 40, 0, 0, time.Local)),
							nil
					}),
				nil, nil)

			_, err = waiters.NewTimeWaiter(mockHub, ep, pastEDef, "", enginert.Default())
			require.Error(t, err)

			// negative cycles
			negativeCyclesEDef := events.MustTimerEventDefinition(
				nil,
				goexpr.Must(
					nil,
					data.MustItemDefinition(
						values.NewVariable(0)),
					func(_ context.Context, ds data.Source) (data.Value, error) {
						return values.NewVariable(-1),
							nil
					}),
				goexpr.Must(
					nil,
					data.MustItemDefinition(
						values.NewVariable(time.Second)),
					func(_ context.Context, ds data.Source) (data.Value, error) {
						return values.NewVariable(time.Second),
							nil
					}))

			_, err = waiters.NewTimeWaiter(mockHub, ep, negativeCyclesEDef, "", enginert.Default())
			require.Error(t, err)

			// negative duration
			negativeDurationEDef := events.MustTimerEventDefinition(
				nil,
				goexpr.Must(
					nil,
					data.MustItemDefinition(
						values.NewVariable(0)),
					func(_ context.Context, ds data.Source) (data.Value, error) {
						return values.NewVariable(1),
							nil
					}),
				goexpr.Must(
					nil,
					data.MustItemDefinition(
						values.NewVariable(time.Second)),
					func(_ context.Context, ds data.Source) (data.Value, error) {
						return values.NewVariable((-1) * time.Second),
							nil
					}))

			_, err = waiters.NewTimeWaiter(mockHub, ep, negativeDurationEDef, "", enginert.Default())
			require.Error(t, err)

			// invalid expression time type value
			require.Panics(t, func() {
				_ = events.MustTimerEventDefinition(
					goexpr.Must(
						nil,
						data.MustItemDefinition(
							values.NewVariable("")),
						func(_ context.Context, ds data.Source) (data.Value, error) {
							return values.NewVariable("invalid type"),
								nil
						}),
					nil, nil)
			})

			// event procesor failure
			oneSecondsEDef := events.MustTimerEventDefinition(
				goexpr.Must(
					nil,
					data.MustItemDefinition(
						values.NewVariable(time.Now())),
					func(_ context.Context, ds data.Source) (data.Value, error) {
						return values.NewVariable(time.Now().Add(time.Second)),
							nil
					}),
				nil, nil)

			w, err := waiters.NewTimeWaiter(mockHub, ep, oneSecondsEDef, "one_seconds_timer", enginert.Default())
			require.NoError(t, err)

			require.NoError(t, w.Service(context.Background()))
			time.Sleep(2 * time.Second)
			require.Equal(t, eventproc.WSFailed, w.State())
		})

	t.Run("stopping and cancellation",
		func(t *testing.T) {
			ep := mockeventproc.NewMockEventProcessor(t)
			mockHub := mockeventproc.NewMockEventHub(t)

			tenSecondsEDef := events.MustTimerEventDefinition(
				goexpr.Must(
					nil,
					data.MustItemDefinition(
						values.NewVariable(time.Now())),
					func(_ context.Context, ds data.Source) (data.Value, error) {
						return values.NewVariable(time.Now().Add(10 * time.Second)),
							nil
					}),
				nil, nil)

			// context cancellation
			wcc, err := waiters.NewTimeWaiter(
				mockHub, ep, tenSecondsEDef, "cancelled by context timer",
				enginert.Default())
			require.NoError(t, err)

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			require.NoError(t, wcc.Service(ctx))
			time.Sleep(4 * time.Second)
			require.Equal(t, eventproc.WSStopped, wcc.State())

			// waiter stopping
			ws, err := waiters.NewTimeWaiter(mockHub, ep, tenSecondsEDef, "stopped timer", enginert.Default())
			require.NoError(t, err)
			require.NoError(t, ws.Service(context.Background()))
			time.Sleep(3 * time.Second)
			require.NoError(t, ws.Stop())
			require.Equal(t, eventproc.WSStopped, ws.State())

			// Done closes once the service goroutine has exited (SRD-019:
			// EventHub.Shutdown drains waiters via this channel).
			select {
			case <-ws.Done():
			case <-time.After(time.Second):
				t.Fatal("timer waiter Done did not close after Stop")
			}
		})

	t.Run("normal",
		func(t *testing.T) {
			ep := mockeventproc.NewMockEventProcessor(t)
			mockHub := mockeventproc.NewMockEventHub(t)

			// time expression
			timeEDef := events.MustTimerEventDefinition(
				goexpr.Must(
					nil,
					data.MustItemDefinition(
						values.NewVariable(time.Now())),
					func(_ context.Context, ds data.Source) (data.Value, error) {
						return values.NewVariable(time.Now().Add(2 * time.Second)), nil
					}),
				nil, nil)

			w, err := waiters.CreateWaiter(mockHub, ep, timeEDef, enginert.Default())
			require.NoError(t, err)
			require.NotEmpty(t, w.ID())
			require.Contains(t, w.EventProcessors(), ep)
			require.Equal(t, timeEDef, w.EventDefinition())

			err = w.Stop()
			require.Error(t, err)
		})

	t.Run("single time",
		func(t *testing.T) {
			ept := mockeventproc.NewMockEventProcessor(t)
			mockHub := mockeventproc.NewMockEventHub(t)
			mockHub.EXPECT().
				WaiterFired(mock.Anything).
				Return(nil).
				Maybe()
			ept.EXPECT().
				ProcessEvent(
					mock.Anything,
					mock.Anything).
				RunAndReturn(
					func(_ context.Context, ed flow.EventDefinition) error {
						t.Log("   >>>> got event: ", ed.Type(), " #", ed.ID())
						return nil
					})

			// time expression
			timeEDef := events.MustTimerEventDefinition(
				goexpr.Must(
					nil,
					data.MustItemDefinition(
						values.NewVariable(time.Now())),
					func(_ context.Context, ds data.Source) (data.Value, error) {
						return values.NewVariable(time.Now().Add(2 * time.Second)), nil
					}),
				nil, nil)

			w, err := waiters.CreateWaiter(mockHub, ept, timeEDef, enginert.Default())
			require.NoError(t, err)
			require.Equal(t, eventproc.WSReady, w.State())
			require.NotEmpty(t, w.ID())

			err = w.Stop()
			require.Error(t, err)

			err = w.Service(context.Background())
			require.NoError(t, err)

			time.Sleep(3 * time.Second)

			err = w.Stop()
			require.Error(t, err)

			require.Error(t, w.Service(context.Background()))
		})

	t.Run("cycle events",
		func(t *testing.T) {
			const cycles = 3

			clk := clocktest.New(time.Now())
			rt := enginert.Default().WithClock(clk)

			got := make(chan struct{}, cycles+1)
			epc := mockeventproc.NewMockEventProcessor(t)
			mockHub := mockeventproc.NewMockEventHub(t)
			mockHub.EXPECT().
				WaiterFired(mock.Anything).
				Return(nil).
				Maybe()
			epc.EXPECT().
				ProcessEvent(mock.Anything, mock.Anything).
				RunAndReturn(
					func(_ context.Context, ed flow.EventDefinition) error {
						t.Log("   >>>> got cycle event: ", ed.Type(), " #", ed.ID())
						got <- struct{}{}

						return nil
					})

			// a Cycle of N with a one-second interval. After FIX-012 a Cycle of
			// N delivers EXACTLY N events (was N+1), so the def is fed N, not
			// N-1.
			cyclesEDef := events.MustTimerEventDefinition(
				nil,
				goexpr.Must(
					nil,
					data.MustItemDefinition(
						values.NewVariable(0)),
					func(context.Context, data.Source) (data.Value, error) {
						return values.NewVariable(cycles), nil
					}),
				goexpr.Must(
					nil,
					data.MustItemDefinition(
						values.NewVariable(time.Second)),
					func(context.Context, data.Source) (data.Value, error) {
						return values.NewVariable(time.Second), nil
					}))

			w, err := waiters.CreateWaiter(mockHub, epc, cyclesEDef, rt)
			require.NoError(t, err)
			require.Equal(t, eventproc.WSReady, w.State())

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			require.NoError(t, w.Service(ctx))

			// drive exactly N cycles deterministically via the fake clock.
			for range cycles {
				advanceUntilFire(t, clk, got, time.Second)
			}

			// no (N+1)th fire: the waiter ended on the Nth delivery, so
			// advancing the clock further yields nothing.
			clk.Advance(time.Second)

			select {
			case <-got:
				t.Fatal("cyclic timer fired more than the requested N times")
			case <-time.After(50 * time.Millisecond):
			}
		})
}

// advanceUntilFire advances the fake clock by step until exactly one timer
// event lands on got, tolerating the service goroutine not having re-armed its
// Clock().After() yet (clocktest does not expose pending timers). The waiter is
// single-goroutine, so at most one After is pending at a time and over-advancing
// in the retry cannot double-fire. Bounded so a never-firing timer fails fast
// instead of hanging.
func advanceUntilFire(
	t *testing.T,
	clk *clocktest.Clock,
	got <-chan struct{},
	step time.Duration,
) {
	t.Helper()

	deadline := time.After(2 * time.Second)
	for {
		clk.Advance(step)

		select {
		case <-got:
			return

		case <-deadline:
			t.Fatal("cyclic timer did not fire within the deadline")

		case <-time.After(5 * time.Millisecond):
			// goroutine may not have re-armed After() yet; advance again.
		}
	}
}

// TestTimerWaiterStopCtxRace is the regression for the double-close panic
// (audit 1.3 / FIX-003 A): a running waiter has ctx cancelled and Stop()
// called concurrently. Under the old code both closed tw.stopCh — a
// "close of closed channel" panic; now Stop() is the single closer. Run
// under -race; repeated to make the interleaving likely.
func TestTimerWaiterStopCtxRace(t *testing.T) {
	ep := mockeventproc.NewMockEventProcessor(t)
	mockHub := mockeventproc.NewMockEventHub(t)

	// a far-future timer so it never fires during the test.
	farEDef := func() flow.EventDefinition {
		return events.MustTimerEventDefinition(
			goexpr.Must(
				nil,
				data.MustItemDefinition(values.NewVariable(time.Now())),
				func(_ context.Context, _ data.Source) (data.Value, error) {
					return values.NewVariable(time.Now().Add(time.Hour)), nil
				}),
			nil, nil)
	}

	for range 50 {
		w, err := waiters.NewTimeWaiter(
			mockHub, ep, farEDef(), "race timer", enginert.Default())
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		require.NoError(t, w.Service(ctx))

		var wg sync.WaitGroup

		wg.Add(2)

		go func() { defer wg.Done(); cancel() }()
		go func() { defer wg.Done(); _ = w.Stop() }()

		wg.Wait()
	}
}

// TestTimerWaiterRemoveEventProcessor exercises the real RemoveEventProcessor
// (FIX-003 B): remove one of several, removing an unknown processor errors
// (ObjectNotFound), and the list empties when the last one leaves.
func TestTimerWaiterRemoveEventProcessor(t *testing.T) {
	ep1 := mockeventproc.NewMockEventProcessor(t)
	ep1.EXPECT().ID().Return("ep-1").Maybe()
	ep2 := mockeventproc.NewMockEventProcessor(t)
	ep2.EXPECT().ID().Return("ep-2").Maybe()
	other := mockeventproc.NewMockEventProcessor(t)
	other.EXPECT().ID().Return("other").Maybe()
	mockHub := mockeventproc.NewMockEventHub(t)

	farEDef := events.MustTimerEventDefinition(
		goexpr.Must(
			nil,
			data.MustItemDefinition(values.NewVariable(time.Now())),
			func(_ context.Context, _ data.Source) (data.Value, error) {
				return values.NewVariable(time.Now().Add(time.Hour)), nil
			}),
		nil, nil)

	w, err := waiters.NewTimeWaiter(
		mockHub, ep1, farEDef, "remove timer", enginert.Default())
	require.NoError(t, err)

	// nil processor rejected on add too.
	require.Error(t, w.AddEventProcessor(nil))

	require.NoError(t, w.AddEventProcessor(ep2))
	require.Len(t, w.EventProcessors(), 2)

	// removing an unregistered processor is an ObjectNotFound error.
	err = w.RemoveEventProcessor(other)
	require.Error(t, err)

	var ae *errs.ApplicationError

	require.True(t, errors.As(err, &ae))
	require.True(t, ae.HasClass(errs.ObjectNotFound))

	// nil processor rejected.
	require.Error(t, w.RemoveEventProcessor(nil))

	// remove one -> one left.
	require.NoError(t, w.RemoveEventProcessor(ep1))
	require.Len(t, w.EventProcessors(), 1)
	require.Contains(t, w.EventProcessors(), ep2)

	// remove the last -> empty.
	require.NoError(t, w.RemoveEventProcessor(ep2))
	require.Empty(t, w.EventProcessors())
}

// TestTimerWaiterServiceRejectsNonReady covers the FIX-012 diagnostic fix: a
// Service call on a waiter that is not WSReady returns an error whose
// current_state diagnostic is the ACTUAL state (WSRunned, after the first
// Service), not the expected WSReady — the latter now lands under
// expected_state. Previously current_state reported WSReady, hiding the real
// state.
func TestTimerWaiterServiceRejectsNonReady(t *testing.T) {
	ep := mockeventproc.NewMockEventProcessor(t)
	mockHub := mockeventproc.NewMockEventHub(t)

	// a far-future timer so the service goroutine parks and never fires.
	farEDef := events.MustTimerEventDefinition(
		goexpr.Must(
			nil,
			data.MustItemDefinition(values.NewVariable(time.Now())),
			func(_ context.Context, _ data.Source) (data.Value, error) {
				return values.NewVariable(time.Now().Add(time.Hour)), nil
			}),
		nil, nil)

	w, err := waiters.NewTimeWaiter(
		mockHub, ep, farEDef, "not-ready timer", enginert.Default())
	require.NoError(t, err)
	require.Equal(t, eventproc.WSReady, w.State())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// first Service moves the waiter to WSRunned.
	require.NoError(t, w.Service(ctx))
	require.Equal(t, eventproc.WSRunned, w.State())

	// second Service hits the not-ready guard.
	err = w.Service(ctx)
	require.Error(t, err)

	var ae *errs.ApplicationError

	require.True(t, errors.As(err, &ae))
	require.True(t, ae.HasClass(errs.InvalidState))
	require.Equal(t, eventproc.WSRunned, ae.Details["current_state"])
	require.Equal(t, eventproc.WSReady, ae.Details["expected_state"])
}

// TestTimerWaiterHonorsInjectedClock is the FIX-012 P1 regression: the service
// goroutine must wait on the injected runtime Clock, not the real wall clock.
// A one-hour Duration timer is driven to fire in milliseconds by advancing a
// clocktest.Clock — under the former time.NewTicker(tw.duration) the ticker
// would never tick within the test and this would time out.
//
// The bounded Advance-then-poll loop exists because clocktest does not expose
// its pending timers, so the test cannot observe the exact instant the
// goroutine registers After(); it advances repeatedly until the fire is seen.
// This completes in one or two scheduling quanta (~ms), NOT the one-hour
// duration.
func TestTimerWaiterHonorsInjectedClock(t *testing.T) {
	clk := clocktest.New(time.Now())
	rt := enginert.Default().WithClock(clk)

	fired := make(chan struct{}, 1)
	ep := mockeventproc.NewMockEventProcessor(t)
	ep.EXPECT().
		ProcessEvent(mock.Anything, mock.Anything).
		RunAndReturn(func(context.Context, flow.EventDefinition) error {
			select {
			case fired <- struct{}{}:
			default:
			}

			return nil
		})

	mockHub := mockeventproc.NewMockEventHub(t)
	mockHub.EXPECT().WaiterFired(mock.Anything).Return(nil).Maybe()

	// a one-shot absolute Time timer one hour ahead of the injected clock.
	// Service computes the delay as next.Sub(Clock().Now()) == one hour, so the
	// wait honours the fake clock end-to-end.
	hourEDef := events.MustTimerEventDefinition(
		goexpr.Must(
			nil,
			data.MustItemDefinition(values.NewVariable(time.Now())),
			func(context.Context, data.Source) (data.Value, error) {
				return values.NewVariable(clk.Now().Add(time.Hour)), nil
			}),
		nil, nil)

	w, err := waiters.CreateWaiter(mockHub, ep, hourEDef, rt)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, w.Service(ctx))

	deadline := time.After(2 * time.Second)
	for {
		clk.Advance(time.Hour)

		select {
		case <-fired:
			return // fired via the injected clock, no real one-hour wait

		case <-deadline:
			t.Fatal("timer never fired under the injected clock — " +
				"wall clock not honoured")

		case <-time.After(5 * time.Millisecond):
			// the goroutine may not have registered After() yet; advance again.
		}
	}
}
