package waiters_test

import (
	"context"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
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

	signalEDef := events.MustSignalEventDefinition(
		&events.Signal{
			BaseElement: *foundation.MustBaseElement(),
		})

	// invalid parameeters
	_, err := waiters.CreateWaiter(nil, ep, timeEDef, enginert.Default())
	require.Error(t, err)
	_, err = waiters.CreateWaiter(mockHub, nil, timeEDef, enginert.Default())
	require.Error(t, err)
	_, err = waiters.CreateWaiter(mockHub, ep, nil, enginert.Default())
	require.Error(t, err)

	_, err = waiters.CreateWaiter(mockHub, ep, signalEDef, enginert.Default())
	require.Error(t, err)
}
