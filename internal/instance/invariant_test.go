package instance

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
)

// failInvariant is the loop's reaction to a broken engine-internal contract:
// fault the instance, stop every track, and answer false so a bool-returning
// guard reads as one line. The guards that call it cannot be driven from any
// input — that is what makes them invariants — but the helper itself can, and
// this is what keeps the FIX-034 §3.2.3 coverage exclusion honest: the excluded
// call sites are one line each, and the behaviour behind them is tested here.
func TestFailInvariantFaultsAndStops(t *testing.T) {
	var (
		mu  sync.Mutex
		log []string
	)

	// A completed iteration is fully wired — Run set up the reporter and the
	// cancel func that fail() and stopAll() use.
	inst := adHocInstance(t, &scriptedRouter{}, &mu, &log)
	runToDone(t, inst)

	ls := newLoopState(inst)

	require.False(t, ls.failInvariant("cache entry for %s is %T", "widget", 42),
		"it answers false so a guard can return it directly")
	require.True(t, ls.stopping, "every track is stopped")

	err := inst.LastErr()
	require.Error(t, err)

	var ae *errs.ApplicationError

	require.ErrorAs(t, err, &ae)
	require.True(t, ae.HasClass(errs.BrokenInvariant),
		"the fault carries the invariant class, not a generic state error")
	require.Contains(t, err.Error(), "cache entry for widget is int")
}

// The two conditional arming guards ARE reachable from a test, unlike the
// boundary-fire one: a watch is a plain struct, so it can be built with a
// definition of the wrong kind — which is exactly the engine-wiring mistake
// they exist to catch.
func TestCondArmingRejectsAForeignDefinition(t *testing.T) {
	var (
		mu  sync.Mutex
		log []string
	)

	sig, err := events.NewSignal("wrong-kind", nil)
	require.NoError(t, err)

	sdef, err := events.NewSignalEventDefinition(sig)
	require.NoError(t, err)

	t.Run("boundary", func(t *testing.T) {
		inst := adHocInstance(t, &scriptedRouter{}, &mu, &log)
		runToDone(t, inst)

		ls := newLoopState(inst)

		require.False(t, ls.armCondBoundary(t.Context(),
			&boundaryWatch{def: sdef}))
		require.True(t, ls.stopping)
		require.ErrorContains(t, inst.LastErr(), "conditional boundary armed")
	})

	t.Run("scope handler", func(t *testing.T) {
		inst := adHocInstance(t, &scriptedRouter{}, &mu, &log)
		runToDone(t, inst)

		ls := newLoopState(inst)

		ls.armCondScopeHandler(t.Context(), &scopeHandlerWatch{
			inst: inst,
			def:  sdef,
		})

		require.True(t, ls.stopping)
		require.ErrorContains(t, inst.LastErr(), "conditional handler armed")
	})
}
