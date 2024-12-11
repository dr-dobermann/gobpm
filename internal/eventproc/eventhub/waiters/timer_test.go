package waiters_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/internal/eventproc/eventhub/waiters"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/monitor"
	"github.com/dr-dobermann/gobpm/pkg/monitor/logmon"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestTimeWaiter(t *testing.T) {
	t.Run("errors",
		func(t *testing.T) {
			ep := mockeventproc.NewMockEventProcessor(t)

			ep.EXPECT().
				ProcessEvent(mock.Anything, mock.Anything).
				RunAndReturn(
					func(context.Context, flow.EventDefinition) error {
						return fmt.Errorf("event processing error")
					}).Maybe()

			// empty parameters
			_, err := waiters.NewTimeWaiter(nil, nil, "")
			require.Error(t, err)

			// invalid event definition
			_, err = waiters.NewTimeWaiter(ep,
				events.MustSignalEventDefinition(
					&events.Signal{}), "")
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

			_, err = waiters.NewTimeWaiter(ep, failiEDef, "")
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

			_, err = waiters.NewTimeWaiter(ep, pastEDef, "")
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

			_, err = waiters.NewTimeWaiter(ep, negativeCyclesEDef, "")
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

			_, err = waiters.NewTimeWaiter(ep, negativeDurationEDef, "")
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

			w, err := waiters.NewTimeWaiter(ep, oneSecondsEDef, "one_seconds_timer")
			require.NoError(t, err)

			require.NoError(t, w.Service(context.Background()))
			time.Sleep(2 * time.Second)
			require.Equal(t, eventproc.WSFailed, w.State())
		})

	t.Run("stopping and cancellation",
		func(t *testing.T) {
			ep := mockeventproc.NewMockEventProcessor(t)

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
				ep, tenSecondsEDef, "cancelled by context timer")
			require.NoError(t, err)

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			require.NoError(t, wcc.Service(ctx))
			time.Sleep(4 * time.Second)
			require.Equal(t, eventproc.WSCancelled, wcc.State())

			// waiter stopping
			ws, err := waiters.NewTimeWaiter(ep, tenSecondsEDef, "stopped timer")
			require.NoError(t, err)
			require.NoError(t, ws.Service(context.Background()))
			time.Sleep(3 * time.Second)
			require.NoError(t, ws.Stop())
			require.Equal(t, eventproc.WSStopped, ws.State())
		})

	t.Run("normal",
		func(t *testing.T) {
			ep := mockeventproc.NewMockEventProcessor(t)

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

			w, err := waiters.CreateWaiter(ep, timeEDef)
			require.NoError(t, err)
			require.NotEmpty(t, w.Id())
			require.Equal(t, ep, w.EventProcessor())
			require.Equal(t, timeEDef, w.EventDefinition())

			err = w.Stop()
			require.Error(t, err)
		})

	t.Run("single time",
		func(t *testing.T) {
			ept := mockeventproc.NewMockEventProcessor(t)
			ept.EXPECT().
				ProcessEvent(
					mock.Anything,
					mock.Anything).
				RunAndReturn(
					func(_ context.Context, ed flow.EventDefinition) error {
						t.Log("   >>>> got event: ", ed.Type(), " #", ed.Id())
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

			// monitoring
			m, err := logmon.New(
				slog.New(
					slog.NewJSONHandler(
						os.Stderr,
						&slog.HandlerOptions{
							Level: slog.LevelDebug,
						})))
			require.NoError(t, err)

			w, err := waiters.CreateWaiter(ept, timeEDef)
			require.NoError(t, err)
			require.Equal(t, eventproc.WSReady, w.State())
			require.NotEmpty(t, w.Id())

			err = w.Stop()
			require.Error(t, err)

			mCtx := context.WithValue(context.Background(), monitor.Key, m)

			err = w.Service(mCtx)
			require.NoError(t, err)

			time.Sleep(3 * time.Second)

			err = w.Stop()
			require.Error(t, err)

			require.Error(t, w.Service(context.Background()))
		})

	t.Run("cycle events",
		func(t *testing.T) {
			cycles := 3
			epc := mockeventproc.NewMockEventProcessor(t)
			epc.EXPECT().
				ProcessEvent(
					mock.AnythingOfType("backgroundCtx"),
					mock.Anything).
				RunAndReturn(
					func(_ context.Context, ed flow.EventDefinition) error {
						t.Log("   >>>> got cycle event: ", ed.Type(), " #", ed.Id())

						require.NotEqual(t, 0, cycles)
						cycles--

						return nil
					})

			cyclesEDef := events.MustTimerEventDefinition(
				nil,
				goexpr.Must(
					nil,
					data.MustItemDefinition(
						values.NewVariable(0)),
					func(ctx context.Context, ds data.Source) (data.Value, error) {
						return values.NewVariable(cycles - 1), nil
					}),
				goexpr.Must(
					nil,
					data.MustItemDefinition(
						values.NewVariable(time.Second)),
					func(ctx context.Context, ds data.Source) (data.Value, error) {
						return values.NewVariable(time.Second), nil
					}))

			w, err := waiters.CreateWaiter(epc, cyclesEDef)
			require.NoError(t, err)
			require.Equal(t, eventproc.WSReady, w.State())

			err = w.Service(context.Background())
			require.NoError(t, err)

			time.Sleep(7 * time.Second)
		})
}
