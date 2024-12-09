package waiters_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/internal/eventproc/eventhub/waiters"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/require"
)

func TestNewWaiter(t *testing.T) {
	ep := mockeventproc.NewMockEventProcessor(t)
	timeEDef := events.MustTimerEventDefinition(
		goexpr.Must(
			nil,
			data.MustItemDefinition(
				values.NewVariable(time.Now())),
			func(ctx context.Context, ds data.Source) (data.Value, error) {
				fmt.Printf("calculating next event time...")
				return values.NewVariable(time.Now().Add(10 * time.Second)), nil
			}), nil, nil)

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

	t.Run("time waiter",
		func(t *testing.T) {
			w, err := waiters.CreateWaiter(ep, timeEDef)
			require.NoError(t, err)
			require.Equal(t, eventproc.WSReady, w.State())
			require.NotEmpty(t, w.Id())

			err = w.Stop()
			require.Error(t, err)

			err = w.Service(context.Background())
			require.NoError(t, err)
		})
}
