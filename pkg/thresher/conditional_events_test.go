package thresher_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/stretchr/testify/require"
)

// SRD-048 e2e — conditional events through the public engine surface: a
// task's committed output flips an intermediate conditional catch and an
// interrupting conditional boundary, with dependency-declared expressions.

// commitTask builds an in-process task committing name=value at its activity
// boundary — the commit-diff signal a conditional subscription re-evaluates
// on (ADR-011 v.6 §2.9.4 → ADR-006 v.3 §2.7).
func commitTask(
	t *testing.T, taskName, dataName string, value int,
) *activities.ServiceTask {
	t.Helper()

	op, err := gooper.New(taskName,
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			return data.MustItemDefinition(
				values.NewVariable(value),
				foundation.WithID(dataName)), nil
		})
	require.NoError(t, err)

	st, err := activities.NewServiceTask(taskName, op,
		activities.WithoutParams())
	require.NoError(t, err)

	return st
}

// watchGt builds the condition "name > n" with a declared dependency on name
// (goexpr.WithDependencies — the SRD-048 narrowing statement).
func watchGt(t *testing.T, name string, n int) data.FormalExpression {
	t.Helper()

	c, err := goexpr.New(nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			d, err := ds.Find(ctx, name)
			if err != nil {
				return nil, err
			}

			v, _ := d.Value().Get(ctx).(int)

			return values.NewVariable(v > n), nil
		},
		goexpr.WithDependencies(name))
	require.NoError(t, err)

	return c
}

// condCatchProcess builds a process where one parallel branch commits the
// value a conditional catch on the other branch waits for:
//
//	start → prep → { raise(total=150) → end-r,
//	                 catch[total>100] → notify → end-n }
func condCatchProcess(t *testing.T, notify *atomic.Bool) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	proc, err := process.New("cond-catch-e2e",
		data.WithProperties(
			data.MustProperty("total",
				data.MustItemDefinition(values.NewVariable(10),
					foundation.WithID("total")),
				data.ReadyDataState)))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	prep := commitTask(t, "prep", "prep-mark", 1)
	raise := commitTask(t, "raise", "total", 150)

	catch, err := events.NewIntermediateCatchEvent("watch-total",
		events.MustConditionalEventDefinition(watchGt(t, "total", 100)))
	require.NoError(t, err)

	nTask := laneTask(t, "notify", notify)

	endR, err := events.NewEndEvent("end-r")
	require.NoError(t, err)
	endN, err := events.NewEndEvent("end-n")
	require.NoError(t, err)

	for _, e := range []flow.Element{
		start, prep, raise, catch, nTask, endR, endN,
	} {
		require.NoError(t, proc.Add(e))
	}

	link(t, start, prep)
	link(t, prep, raise)
	link(t, prep, catch)
	link(t, raise, endR)
	link(t, catch, nTask)
	link(t, nTask, endN)

	return proc
}

// condBoundaryProcess builds a blocked host guarded by an interrupting
// conditional boundary a sibling branch releases:
//
//	start → prep → { host(ReceiveTask, ⚡[flag>0] → exc → end-e),
//	                 raise(flag=1) → end-r }
func condBoundaryProcess(t *testing.T, exc *atomic.Bool) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	proc, err := process.New("cond-boundary-e2e",
		data.WithProperties(
			data.MustProperty("flag",
				data.MustItemDefinition(values.NewVariable(0),
					foundation.WithID("flag")),
				data.ReadyDataState)))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	prep := commitTask(t, "prep", "prep-mark", 1)

	host, err := activities.NewReceiveTask("host",
		bpmncommon.MustMessage("never-sent",
			data.MustItemDefinition(values.NewVariable(1))))
	require.NoError(t, err)

	bnd, err := events.NewBoundaryEvent("released", host,
		events.MustConditionalEventDefinition(watchGt(t, "flag", 0)),
		true) // interrupting
	require.NoError(t, err)

	eTask := laneTask(t, "exception", exc)

	raise := commitTask(t, "raise", "flag", 1)

	endR, err := events.NewEndEvent("end-r")
	require.NoError(t, err)
	endE, err := events.NewEndEvent("end-e")
	require.NoError(t, err)

	for _, e := range []flow.Element{
		start, prep, host, bnd, eTask, raise, endR, endE,
	} {
		require.NoError(t, proc.Add(e))
	}

	link(t, start, prep)
	link(t, prep, host)
	link(t, prep, raise)
	link(t, raise, endR)
	link(t, bnd, eTask)
	link(t, eTask, endE)

	return proc
}

// TestConditionalEventsE2E (SRD-048 §6): data-driven waiting without polling —
// a committed change releases a conditional catch; an interrupting
// conditional boundary cancels a blocked host and routes its exception flow.
func TestConditionalEventsE2E(t *testing.T) {
	t.Run("intermediate catch released by a sibling commit", func(t *testing.T) {
		var notify atomic.Bool

		require.NoError(t, runFlows(t, condCatchProcess(t, &notify)))
		require.True(t, notify.Load(),
			"the conditional catch must release the notify path")
	})

	t.Run("interrupting boundary releases a blocked host", func(t *testing.T) {
		var exc atomic.Bool

		require.NoError(t, runFlows(t, condBoundaryProcess(t, &exc)))
		require.True(t, exc.Load(),
			"the boundary's exception path must run")
	})
}
