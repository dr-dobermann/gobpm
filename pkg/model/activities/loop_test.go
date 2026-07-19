package activities_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/stretchr/testify/require"
)

// boolExpr builds a FormalExpression whose result type is bool — a valid
// loopCondition.
func boolExpr(t *testing.T) data.FormalExpression {
	t.Helper()

	return goexpr.Must(nil,
		data.MustItemDefinition(values.NewVariable(true)),
		func(_ context.Context, _ data.Source) (data.Value, error) {
			return values.NewVariable(true), nil
		})
}

// intExpr builds a FormalExpression whose result type is NOT bool — an invalid
// loopCondition.
func intExpr(t *testing.T) data.FormalExpression {
	t.Helper()

	return goexpr.Must(nil,
		data.MustItemDefinition(values.NewVariable(1)),
		func(_ context.Context, _ data.Source) (data.Value, error) {
			return values.NewVariable(1), nil
		})
}

// mustStdLoop builds a minimal valid Standard Loop.
func mustStdLoop(t *testing.T) *activities.StandardLoopCharacteristics {
	t.Helper()

	sl, err := activities.NewStandardLoop(boolExpr(t))
	require.NoError(t, err)

	return sl
}

// TestStandardLoopBuildAndAccessors (SRD-054 FR-1): the constructor stores the
// condition and options, and the accessors read them back — including the
// unset-option defaults (post-tested, unbounded).
func TestStandardLoopBuildAndAccessors(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	t.Run("defaults: post-tested, unbounded", func(t *testing.T) {
		sl, err := activities.NewStandardLoop(boolExpr(t))
		require.NoError(t, err)
		require.NotNil(t, sl.LoopCondition())
		require.False(t, sl.TestBefore())
		max, ok := sl.LoopMaximum()
		require.False(t, ok)
		require.Equal(t, 0, max)
	})

	t.Run("options: pre-tested, capped", func(t *testing.T) {
		sl, err := activities.NewStandardLoop(boolExpr(t),
			activities.WithTestBefore(),
			activities.WithLoopMaximum(3))
		require.NoError(t, err)
		require.True(t, sl.TestBefore())
		max, ok := sl.LoopMaximum()
		require.True(t, ok)
		require.Equal(t, 3, max)
	})
}

// TestStandardLoopRejectsNilCondition (FR-2): a nil loopCondition is rejected.
func TestStandardLoopRejectsNilCondition(t *testing.T) {
	sl, err := activities.NewStandardLoop(nil)
	require.Error(t, err)
	require.Nil(t, sl)
	require.Contains(t, err.Error(), "loopCondition")
}

// TestStandardLoopRejectsNonBoolCondition (FR-2): a non-boolean loopCondition
// is rejected.
func TestStandardLoopRejectsNonBoolCondition(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	sl, err := activities.NewStandardLoop(intExpr(t))
	require.Error(t, err)
	require.Nil(t, sl)
	require.Contains(t, err.Error(), "boolean")
}

// TestStandardLoopMaximumMustBePositive (FR-2): a zero or negative loopMaximum
// is rejected.
func TestStandardLoopMaximumMustBePositive(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	for _, n := range []int{0, -1} {
		sl, err := activities.NewStandardLoop(boolExpr(t),
			activities.WithLoopMaximum(n))
		require.Error(t, err, "maximum %d", n)
		require.Nil(t, sl)
		require.Contains(t, err.Error(), "positive")
	}
}

// TestActivityLoopMarkerIsSingle (FR-3): an activity holds a single loop marker
// — no loop yields nil, and a later WithLoop replaces an earlier one (so a
// Standard Loop and a Multi-Instance marker can never coexist).
func TestActivityLoopMarkerIsSingle(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	noLoop, err := activities.NewSubProcess("plain")
	require.NoError(t, err)
	require.Nil(t, noLoop.LoopCharacteristics())

	first, second := mustStdLoop(t), mustStdLoop(t)
	sp, err := activities.NewSubProcess("looped",
		activities.WithLoop(first),
		activities.WithLoop(second))
	require.NoError(t, err)
	require.Same(t, second, sp.LoopCharacteristics(),
		"the later WithLoop must replace the earlier marker")
}

// TestEventSubProcessRejectsLoop (FR-3a): a triggeredByEvent Sub-Process that
// carries a loop marker fails validation — an event-instantiated handler has no
// token-driven activation to iterate (ADR-025 §2.2).
func TestEventSubProcessRejectsLoop(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	// a well-formed event sub-process (one interrupting triggered start →
	// task → end) that additionally, and invalidly, carries a Standard Loop.
	es, err := activities.NewSubProcess("loop-es",
		activities.WithTriggeredByEvent(),
		activities.WithLoop(mustStdLoop(t)))
	require.NoError(t, err)

	start := sigStart(t, "es-start", true)
	task := spTask(t, "es-task")
	end, err := events.NewEndEvent("es-end")
	require.NoError(t, err)
	for _, e := range []flow.Element{start, task, end} {
		require.NoError(t, es.Add(e))
	}
	_, err = flow.Link(start, task)
	require.NoError(t, err)
	_, err = flow.Link(task, end)
	require.NoError(t, err)

	err = es.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "must not carry loop")
}
