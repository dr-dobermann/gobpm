package events_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
)

// evaluate runs a definition's expression the way the waiter does, so a test
// asserts the value the engine would actually read.
func evaluate(t *testing.T, fe data.FormalExpression) any {
	t.Helper()

	v, err := fe.Evaluate(context.Background(), nil)
	require.NoError(t, err)

	return v.Get(context.Background())
}

// TestISO8601TimerDisassembly pins that one ISO 8601 string lands in exactly
// the attributes it names, and nothing else (SRD-077 T-6, FR-6).
func TestISO8601TimerDisassembly(t *testing.T) {
	data.CreateDefaultStates()

	t.Run("date-time fills timeDate alone", func(t *testing.T) {
		ted, err := events.NewISO8601Timer("2011-03-11T12:13:14Z")
		require.NoError(t, err)
		require.Nil(t, ted.Cycle())
		require.Nil(t, ted.Duration())
		require.Equal(t,
			time.Date(2011, 3, 11, 12, 13, 14, 0, time.UTC),
			evaluate(t, ted.Time()).(time.Time).UTC())
	})

	t.Run("duration fills timeDuration alone", func(t *testing.T) {
		ted, err := events.NewISO8601Timer("P10D")
		require.NoError(t, err)
		require.Nil(t, ted.Time())
		require.Nil(t, ted.Cycle())
		require.Equal(t, 10*24*time.Hour, evaluate(t, ted.Duration()))
	})

	t.Run("recurrence fills cycle AND duration", func(t *testing.T) {
		ted, err := events.NewISO8601Timer("R3/PT10H")
		require.NoError(t, err)
		require.Nil(t, ted.Time())
		require.Equal(t, 3, evaluate(t, ted.Cycle()))
		require.Equal(t, 10*time.Hour, evaluate(t, ted.Duration()))
	})
}

// TestISO8601TimerRejects proves an unparseable literal names all three
// accepted shapes rather than only the grammar the parser guessed.
func TestISO8601TimerRejects(t *testing.T) {
	data.CreateDefaultStates()

	for _, in := range []string{"tomorrow", "", "P1Y", "R/PT10H", "PT0S"} {
		ted, err := events.NewISO8601Timer(in)
		require.Error(t, err, "%q must be refused", in)
		require.Nil(t, ted)
		require.Contains(t, err.Error(), "not an ISO 8601 timer")
		require.Contains(t, err.Error(), "bounded recurrence")
	}

	require.Panics(t, func() { _ = events.MustISO8601Timer("nonsense") })
	require.NotPanics(t, func() {
		require.NotNil(t, events.MustISO8601Timer("PT5M"))
	})
}

// TestISO8601TimerMatchesPositional proves the string form is sugar, not a
// second mechanism: it yields the same evaluated values as building the
// definition positionally.
func TestISO8601TimerMatchesPositional(t *testing.T) {
	data.CreateDefaultStates()

	fromString, err := events.NewISO8601Timer("R3/PT10H")
	require.NoError(t, err)

	positional := events.MustTimerEventDefinition(
		nil, constOf(t, 3), constOf(t, 10*time.Hour))

	require.Equal(t,
		evaluate(t, positional.Cycle()), evaluate(t, fromString.Cycle()))
	require.Equal(t,
		evaluate(t, positional.Duration()), evaluate(t, fromString.Duration()))
}

// constOf builds a constant expression of v's own type.
func constOf[T any](t *testing.T, v T) data.FormalExpression {
	t.Helper()

	return goexpr.Must(nil,
		data.MustItemDefinition(values.NewVariable(v)),
		func(_ context.Context, _ data.Source) (data.Value, error) {
			return values.NewVariable(v), nil
		})
}

// stringExpr yields whatever *src holds at EVALUATION time, so a test can
// change the answer between arms — which is what "dynamic" has to mean.
func stringExpr(t *testing.T, src *string) data.FormalExpression {
	t.Helper()

	return goexpr.Must(nil,
		data.MustItemDefinition(values.NewVariable("")),
		func(_ context.Context, _ data.Source) (data.Value, error) {
			return values.NewVariable(*src), nil
		})
}

// TestISO8601TimerExprIsEvaluatedPerArm is the point of the expression
// form: the ISO string is read when the timer arms, not when the process is
// built, so a deadline can come from the instance's own data (SRD-077 FR-6).
func TestISO8601TimerExprIsEvaluatedPerArm(t *testing.T) {
	data.CreateDefaultStates()

	sla := "PT5M"

	ted, err := events.NewISO8601TimerExpr(
		events.Duration, stringExpr(t, &sla))
	require.NoError(t, err)
	require.Nil(t, ted.Time())
	require.Nil(t, ted.Cycle())

	require.Equal(t, 5*time.Minute, evaluate(t, ted.Duration()))

	// A later instance carries a different SLA — same definition, new answer.
	sla = "PT2H"
	require.Equal(t, 2*time.Hour, evaluate(t, ted.Duration()))
}

// TestISO8601TimerExprForms covers each form's attribute placement, including
// a recurrence feeding BOTH attributes from one expression.
func TestISO8601TimerExprForms(t *testing.T) {
	data.CreateDefaultStates()

	when := "2011-03-11T12:13:14Z"
	dateDef, err := events.NewISO8601TimerExpr(
		events.Time, stringExpr(t, &when))
	require.NoError(t, err)
	require.Nil(t, dateDef.Duration())
	require.Equal(t, time.Date(2011, 3, 11, 12, 13, 14, 0, time.UTC),
		evaluate(t, dateDef.Time()).(time.Time).UTC())

	every := "R4/PT15M"
	cycleDef, err := events.NewISO8601TimerExpr(
		events.Cycle, stringExpr(t, &every))
	require.NoError(t, err)
	require.Nil(t, cycleDef.Time())
	require.Equal(t, 4, evaluate(t, cycleDef.Cycle()))
	require.Equal(t, 15*time.Minute, evaluate(t, cycleDef.Duration()))
}

// TestISO8601TimerExprFailsAtArm pins where a bad dynamic value surfaces: the
// definition builds, and the failure arrives when the expression is evaluated,
// naming the offending value.
func TestISO8601TimerExprFailsAtArm(t *testing.T) {
	data.CreateDefaultStates()

	bad := "P1Y"

	ted, err := events.NewISO8601TimerExpr(
		events.Duration, stringExpr(t, &bad))
	require.NoError(t, err, "construction cannot know the value yet")

	_, err = ted.Duration().Evaluate(context.Background(), nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "P1Y")
}

// TestISO8601TimerExprGuards covers the construction-time refusals.
func TestISO8601TimerExprGuards(t *testing.T) {
	data.CreateDefaultStates()

	_, err := events.NewISO8601TimerExpr(events.Duration, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "nil expression")

	s := "PT5M"

	_, err = events.NewISO8601TimerExpr("nonsense", stringExpr(t, &s))
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown timer form")

	require.Panics(t, func() {
		_ = events.MustISO8601TimerExpr(events.Duration, nil)
	})
	require.NotPanics(t, func() {
		require.NotNil(t,
			events.MustISO8601TimerExpr(events.Time, stringExpr(t, &s)))
	})
}

// TestISO8601TimerExprAdapterFailures covers the two ways the wrapped
// expression can fail the adapter before any parsing happens: it errors, or it
// yields something that is not a string.
func TestISO8601TimerExprAdapterFailures(t *testing.T) {
	data.CreateDefaultStates()

	t.Run("inner expression errors", func(t *testing.T) {
		boom := goexpr.Must(nil,
			data.MustItemDefinition(values.NewVariable("")),
			func(_ context.Context, _ data.Source) (data.Value, error) {
				return nil, errors.New("data source is unavailable")
			})

		ted, err := events.NewISO8601TimerExpr(events.Duration, boom)
		require.NoError(t, err)

		_, err = ted.Duration().Evaluate(context.Background(), nil)
		require.ErrorContains(t, err, "data source is unavailable")
	})

	t.Run("inner expression yields a non-string", func(t *testing.T) {
		// Declared as an int, so goexpr's own type check passes and the
		// non-string reaches the adapter — which is the guard under test.
		// FormalExpression is an interface, so nothing guarantees a string.
		number := goexpr.Must(nil,
			data.MustItemDefinition(values.NewVariable(0)),
			func(_ context.Context, _ data.Source) (data.Value, error) {
				return values.NewVariable(42), nil
			})

		ted, err := events.NewISO8601TimerExpr(events.Time, number)
		require.NoError(t, err)

		_, err = ted.Time().Evaluate(context.Background(), nil)
		require.ErrorContains(t, err, "must evaluate to an ISO 8601 string")
	})
}
