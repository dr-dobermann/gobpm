package events_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// TestIntermediateCatchDehydratable covers SRD-071 FR-1a: the catch declares
// whether parking on it may release the instance's goroutines. Timer, message
// and signal waits have durable engine holders, so they release; a CONDITIONAL
// wait never does — its trigger is the instance's own data commits, so a
// released instance could never be woken.
func TestIntermediateCatchDehydratable(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	timeExpr, err := goexpr.New(nil,
		data.MustItemDefinition(values.NewVariable(time.Time{})),
		func(context.Context, data.Source) (data.Value, error) {
			return values.NewVariable(time.Now().Add(time.Hour)), nil
		})
	require.NoError(t, err)

	timerDef, err := events.NewTimerEventDefinition(timeExpr, nil, nil)
	require.NoError(t, err)

	sig, err := events.NewSignal("go", nil)
	require.NoError(t, err)

	signalDef, err := events.NewSignalEventDefinition(sig)
	require.NoError(t, err)

	condExpr, err := goexpr.New(nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(context.Context, data.Source) (data.Value, error) {
			return values.NewVariable(true), nil
		})
	require.NoError(t, err)

	condDef, err := events.NewConditionalEventDefinition(condExpr)
	require.NoError(t, err)

	msgDef := events.MustMessageEventDefinition(
		bpmncommon.MustMessage("m", data.MustItemDefinition(
			values.NewVariable(""))), nil)

	for _, tc := range []struct {
		name string
		def  flow.EventDefinition
		want bool
	}{
		{"timer releases", timerDef, true},
		{"message releases", msgDef, true},
		{"signal releases", signalDef, true},
		{"conditional never releases", condDef, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ice, err := events.NewIntermediateCatchEvent("c", tc.def)
			require.NoError(t, err)

			require.Equal(t, tc.want,
				ice.Dehydratable(context.Background(), nil))
		})
	}
}
