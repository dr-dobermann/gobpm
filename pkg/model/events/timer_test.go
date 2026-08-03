package events_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// constExpr builds a FormalExpression evaluating to v, with v's own type as the
// declared result type — the shape NewTimerEventDefinition type-checks against.
func constExpr[T any](v T) data.FormalExpression {
	return goexpr.Must(nil,
		data.MustItemDefinition(values.NewVariable(v)),
		func(_ context.Context, _ data.Source) (data.Value, error) {
			return values.NewVariable(v), nil
		})
}

func timerDate() data.FormalExpression  { return constExpr(time.Now().Add(time.Hour)) }
func timerCycle() data.FormalExpression { return constExpr(3) }
func timerDur() data.FormalExpression   { return constExpr(5 * time.Minute) }

// TestTimerAttributeCombinations pins the accept/reject verdict of every
// combination of the three timer attributes (SRD-077 T-1).
//
// BPMN §10.5.5 Table 10.101 calls the three mutually exclusive; the engine
// carries a recurrence as timeCycle (count) WITH timeDuration (interval)
// rather than as one ISO 8601 string, so that pair is accepted and a lone
// timeCycle is not. Every other verdict matches the standard.
func TestTimerAttributeCombinations(t *testing.T) {
	data.CreateDefaultStates()

	for _, tc := range []struct {
		name             string
		date, cycle, dur bool
		wantOK           bool
		why              string
	}{
		{"date alone", true, false, false, true,
			"an absolute deadline"},
		{"duration alone", false, false, true, true,
			"a relative deadline — fires once after the interval"},
		{"cycle with duration", false, true, true, true,
			"the recurrence: count plus interval"},
		{"cycle alone", false, true, false, false,
			"a repetition count with no interval has nothing to schedule"},
		{"date with duration", true, false, true, false,
			"mutually exclusive"},
		{"date with cycle", true, true, false, false,
			"mutually exclusive"},
		{"all three", true, true, true, false,
			"mutually exclusive"},
		{"none", false, false, false, false,
			"nothing to schedule"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var d, c, u data.FormalExpression

			if tc.date {
				d = timerDate()
			}

			if tc.cycle {
				c = timerCycle()
			}

			if tc.dur {
				u = timerDur()
			}

			ted, err := events.NewTimerEventDefinition(d, c, u)

			if !tc.wantOK {
				require.Error(t, err, tc.why)
				require.Nil(t, ted)

				return
			}

			require.NoError(t, err, tc.why)
			require.NotNil(t, ted)
			require.Equal(t, flow.TriggerTimer, ted.Type())

			// Only the requested attributes are carried.
			require.Equal(t, tc.date, ted.Time() != nil, "timeDate")
			require.Equal(t, tc.cycle, ted.Cycle() != nil, "timeCycle")
			require.Equal(t, tc.dur, ted.Duration() != nil, "timeDuration")
		})
	}
}

// TestTimerGuardErrorNamesTheRule proves each rejection carries its OWN
// message naming the rule broken, so a caller can tell "these attributes are
// mutually exclusive" from "this recurrence is missing its interval"
// (SRD-077 T-2, FR-3). Before this change all three shared one message.
func TestTimerGuardErrorNamesTheRule(t *testing.T) {
	data.CreateDefaultStates()

	_, emptyErr := events.NewTimerEventDefinition(nil, nil, nil)
	require.Error(t, emptyErr)

	_, exclusiveErr := events.NewTimerEventDefinition(
		timerDate(), nil, timerDur())
	require.Error(t, exclusiveErr)

	_, intervalErr := events.NewTimerEventDefinition(nil, timerCycle(), nil)
	require.Error(t, intervalErr)

	require.Contains(t, exclusiveErr.Error(), "mutually")
	require.Contains(t, exclusiveErr.Error(), "Table 10.101")
	require.Contains(t, intervalErr.Error(), "timeCycle needs timeDuration")

	// Distinct messages, not one text reused.
	require.NotEqual(t, emptyErr.Error(), exclusiveErr.Error())
	require.NotEqual(t, exclusiveErr.Error(), intervalErr.Error())

	// Each names the constructor, so the failure is locatable.
	for _, err := range []error{emptyErr, exclusiveErr, intervalErr} {
		require.True(t,
			strings.Contains(err.Error(), "NewTimerEventDefinition"),
			"error must self-identify: %s", err.Error())
	}
}

// TestTimerResultTypeIsChecked covers the per-attribute result-type guard: an
// expression whose declared type doesn't match its attribute is refused.
func TestTimerResultTypeIsChecked(t *testing.T) {
	data.CreateDefaultStates()

	// A duration slot fed a time.Time.
	_, err := events.NewTimerEventDefinition(nil, nil, timerDate())
	require.Error(t, err)

	// A cycle slot fed a duration.
	_, err = events.NewTimerEventDefinition(nil, timerDur(), timerDur())
	require.Error(t, err)
}

// TestMustTimerEventDefinition covers both twin paths: a valid definition is
// returned, an invalid one panics (the Must* contract).
func TestMustTimerEventDefinition(t *testing.T) {
	data.CreateDefaultStates()

	require.NotPanics(t, func() {
		ted := events.MustTimerEventDefinition(nil, nil, timerDur())
		require.NotNil(t, ted)
		require.NotNil(t, ted.Duration())
		require.Nil(t, ted.Time())
		require.Nil(t, ted.Cycle())
	})

	require.Panics(t, func() {
		_ = events.MustTimerEventDefinition(nil, timerCycle(), nil)
	})
}

// TestTimerCloneForInstance pins that a clone shares the expressions but takes
// a fresh id — the per-instance waiter identity FIX-004 established.
func TestTimerCloneForInstance(t *testing.T) {
	data.CreateDefaultStates()

	ted := events.MustTimerEventDefinition(nil, timerCycle(), timerDur())

	clone, ok := ted.CloneForInstance().(*events.TimerEventDefinition)
	require.True(t, ok)
	require.NotEqual(t, ted.ID(), clone.ID(), "clone must take a fresh id")
	require.Equal(t, ted.Cycle(), clone.Cycle())
	require.Equal(t, ted.Duration(), clone.Duration())
	require.Equal(t, ted.Time(), clone.Time())
}

// TestTimerBaseOptionError covers the base-element option path: a valid
// attribute combination still fails when a base option is invalid, and the
// option's own error surfaces rather than being swallowed.
func TestTimerBaseOptionError(t *testing.T) {
	data.CreateDefaultStates()

	ted, err := events.NewTimerEventDefinition(nil, nil, timerDur(),
		foundation.WithID(""))
	require.Error(t, err)
	require.Nil(t, ted)
	require.Contains(t, err.Error(), "empty id isn't allowed")
}
