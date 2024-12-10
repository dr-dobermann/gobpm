package waiters_test

import (
	"context"
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
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/monitor"
	"github.com/dr-dobermann/gobpm/pkg/monitor/logmon"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNewWaiter(t *testing.T) {
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

	signalEDef := events.MustSignalEventDefinition(
		&events.Signal{
			BaseElement: *foundation.MustBaseElement(),
		})

	// invalid parameeters
	_, err := waiters.CreateWaiter(nil, timeEDef)
	require.Error(t, err)
	_, err = waiters.CreateWaiter(ep, nil)
	require.Error(t, err)

	_, err = waiters.CreateWaiter(ep, signalEDef)
	require.Error(t, err)
}

func TestTimeWaiter(t *testing.T) {
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
