package waiters_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/generated/mockflow"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/eventproc/eventhub/waiters"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/require"
)

// timerEDef builds a minimal timer event definition — a non-message, non-signal
// trigger, used to exercise CreatePersistentWaiter's rejection branch.
func timerEDef(t *testing.T) *events.TimerEventDefinition {
	t.Helper()

	return events.MustTimerEventDefinition(
		goexpr.Must(
			nil,
			data.MustItemDefinition(values.NewVariable(time.Now())),
			func(_ context.Context, _ data.Source) (data.Value, error) {
				return values.NewVariable(time.Now().Add(time.Second)), nil
			}),
		nil, nil)
}

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
	// each builder failure carries a classified errs error, not a bare
	// fmt.Errorf (FIX-003 §3.2.1).
	requireClass := func(err error, class string) {
		t.Helper()

		require.Error(t, err)

		var ae *errs.ApplicationError

		require.True(t, errors.As(err, &ae), "error must be an ApplicationError")
		require.True(t, ae.HasClass(class), "error must carry class %q", class)
	}

	_, err := waiters.CreateWaiter(nil, ep, timeEDef, enginert.Default())
	requireClass(err, errs.EmptyNotAllowed)

	_, err = waiters.CreateWaiter(mockHub, nil, timeEDef, enginert.Default())
	requireClass(err, errs.EmptyNotAllowed)

	_, err = waiters.CreateWaiter(mockHub, ep, nil, enginert.Default())
	requireClass(err, errs.EmptyNotAllowed)

	// signal is now supported (SRD-020): CreateWaiter builds a passive
	// signalWaiter.
	w, err := waiters.CreateWaiter(mockHub, ep, signalEDef, enginert.Default())
	require.NoError(t, err)
	require.NotNil(t, w)

	// an unsupported trigger (conditional) still hits the default branch.
	condEDef := mockflow.NewMockEventDefinition(t)
	condEDef.EXPECT().Type().Return(flow.TriggerConditional).Maybe()
	condEDef.EXPECT().ID().Return("cond-1").Maybe()
	_, err = waiters.CreateWaiter(mockHub, ep, condEDef, enginert.Default())
	requireClass(err, errs.ObjectNotFound)
}
