package activities_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/stretchr/testify/require"
)

// spTask builds a minimal in-process service task for sub-process bodies.
func spTask(t *testing.T, name string) *activities.ServiceTask {
	t.Helper()

	op, err := gooper.New(name,
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			return nil, nil
		})
	require.NoError(t, err)

	st, err := activities.NewServiceTask(name, op, activities.WithoutParams())
	require.NoError(t, err)

	return st
}

// noneStartSP builds a sub-process with the unique-None-start shape:
// start → task → end.
func noneStartSP(t *testing.T, name string) *activities.SubProcess {
	t.Helper()

	sp, err := activities.NewSubProcess(name)
	require.NoError(t, err)

	start, err := events.NewStartEvent("start-" + name)
	require.NoError(t, err)

	task := spTask(t, "task-"+name)

	end, err := events.NewEndEvent("end-" + name)
	require.NoError(t, err)

	for _, e := range []flow.Element{start, task, end} {
		require.NoError(t, sp.Add(e))
	}

	_, err = flow.Link(start, task)
	require.NoError(t, err)
	_, err = flow.Link(task, end)
	require.NoError(t, err)

	return sp
}

func TestSubProcessShapes(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	t.Run("unique None start validates", func(t *testing.T) {
		require.NoError(t, noneStartSP(t, "ok").Validate())
	})

	t.Run("flow-less nodes validate", func(t *testing.T) {
		sp, err := activities.NewSubProcess("flowless")
		require.NoError(t, err)

		a := spTask(t, "a")
		b := spTask(t, "b")
		end, err := events.NewEndEvent("end")
		require.NoError(t, err)

		for _, e := range []flow.Element{a, b, end} {
			require.NoError(t, sp.Add(e))
		}

		_, err = flow.Link(a, end)
		require.NoError(t, err)

		require.NoError(t, sp.Validate())
	})

	t.Run("triggered start rejected", func(t *testing.T) {
		sp, err := activities.NewSubProcess("triggered")
		require.NoError(t, err)

		sig, err := events.NewSignal("s",
			data.MustItemDefinition(values.NewVariable(1)))
		require.NoError(t, err)

		start, err := events.NewStartEvent("sig-start",
			events.WithSignalTrigger(events.MustSignalEventDefinition(sig)))
		require.NoError(t, err)

		require.NoError(t, sp.Add(start))

		err = sp.Validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "triggered Start Event")
	})

	t.Run("multiple None starts rejected", func(t *testing.T) {
		sp, err := activities.NewSubProcess("multi")
		require.NoError(t, err)

		for _, n := range []string{"s1", "s2"} {
			start, err := events.NewStartEvent(n)
			require.NoError(t, err)
			require.NoError(t, sp.Add(start))
		}

		err = sp.Validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "unique None Start Event")
	})

	t.Run("mixed shape rejected", func(t *testing.T) {
		sp, err := activities.NewSubProcess("mixed")
		require.NoError(t, err)

		start, err := events.NewStartEvent("start")
		require.NoError(t, err)

		require.NoError(t, sp.Add(start))
		require.NoError(t, sp.Add(spTask(t, "loose")))

		err = sp.Validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "can't be mixed")
	})

	t.Run("empty container rejected", func(t *testing.T) {
		sp, err := activities.NewSubProcess("empty")
		require.NoError(t, err)

		err = sp.Validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "needs an entry")
	})

	t.Run("nested sub-process recurses", func(t *testing.T) {
		outer, err := activities.NewSubProcess("outer")
		require.NoError(t, err)

		inner, err := activities.NewSubProcess("inner") // empty → invalid
		require.NoError(t, err)

		start, err := events.NewStartEvent("start")
		require.NoError(t, err)

		require.NoError(t, outer.Add(start))
		require.NoError(t, outer.Add(inner))

		_, err = flow.Link(start, inner)
		require.NoError(t, err)

		err = outer.Validate()
		require.Error(t, err, "the inner empty sub-process must surface")
		require.Contains(t, err.Error(), "needs an entry")
	})

	t.Run("inner boundary event on inner host validates", func(t *testing.T) {
		sp := noneStartSP(t, "bnd")

		// attach a boundary to the inner task and add it to the container.
		var host flow.ActivityNode
		for _, n := range sp.Nodes() {
			if an, ok := n.(flow.ActivityNode); ok {
				host = an
			}
		}
		require.NotNil(t, host)

		sig, err := events.NewSignal("bnd-sig",
			data.MustItemDefinition(values.NewVariable(1)))
		require.NoError(t, err)
		sdef, err := events.NewSignalEventDefinition(sig)
		require.NoError(t, err)

		be, err := events.NewBoundaryEvent("bnd", host, sdef, true)
		require.NoError(t, err)
		require.NoError(t, sp.Add(be))

		exc, err := events.NewEndEvent("exc")
		require.NoError(t, err)
		require.NoError(t, sp.Add(exc))
		_, err = flow.Link(be, exc)
		require.NoError(t, err)

		require.NoError(t, sp.Validate())
	})
}

func TestSubProcessContainment(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	t.Run("cross-boundary link rejected", func(t *testing.T) {
		sp := noneStartSP(t, "inside")

		outside := spTask(t, "outside") // no container

		var inner flow.Node
		for _, n := range sp.Nodes() {
			if an, ok := n.(flow.ActivityNode); ok {
				inner = an
			}
		}
		require.NotNil(t, inner)

		// linking an inner node to an un-contained one violates the
		// same-container rule the SequenceFlow validation enforces.
		_, err := flow.Link(inner.(flow.SequenceSource), outside)
		require.Error(t, err)
	})

	t.Run("activity surface present", func(t *testing.T) {
		sp := noneStartSP(t, "iface")

		require.Equal(t, flow.SubProcessActivity, sp.ActivityType())
		require.Equal(t, flow.ActivityNodeType, sp.NodeType())

		// the boundary machinery consumes the activity base unchanged.
		sig, err := events.NewSignal("host-sig",
			data.MustItemDefinition(values.NewVariable(1)))
		require.NoError(t, err)
		sdef, err := events.NewSignalEventDefinition(sig)
		require.NoError(t, err)

		be, err := events.NewBoundaryEvent("host-bnd", sp, sdef, true)
		require.NoError(t, err)
		require.Len(t, sp.BoundaryEvents(), 1)
		require.Equal(t, be.ID(), sp.BoundaryEvents()[0].ID())
	})

	t.Run("clone not yet implemented", func(t *testing.T) {
		_, err := noneStartSP(t, "clone").Clone()
		require.Error(t, err)
		require.Contains(t, err.Error(), "isn't implemented yet")
	})
}

func TestElementsContainerAddRemove(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	sp, err := activities.NewSubProcess("core")
	require.NoError(t, err)

	task := spTask(t, "t")

	t.Run("add and duplicate", func(t *testing.T) {
		require.NoError(t, sp.Add(task))
		err := sp.Add(task)
		require.Error(t, err)
		require.Contains(t, err.Error(), "already in the container")
	})

	t.Run("nil element rejected", func(t *testing.T) {
		require.Error(t, sp.Add(nil))
		require.Error(t, sp.Remove(nil))
	})

	t.Run("elements and accessors", func(t *testing.T) {
		require.Len(t, sp.Elements(), 1)
		require.Len(t, sp.Nodes(), 1)
		require.Empty(t, sp.Flows())
	})

	t.Run("remove and missing", func(t *testing.T) {
		require.NoError(t, sp.Remove(task))
		err := sp.Remove(task)
		require.Error(t, err)
		require.Contains(t, err.Error(), "isn't in the container")
		require.Nil(t, task.Container(), "removal unbinds")
	})
}

func TestSubProcessDefensive(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	t.Run("invalid option rejected", func(t *testing.T) {
		_, err := activities.NewSubProcess("bad",
			options.WithName("not an activity option"))
		require.Error(t, err)
	})

	t.Run("dangling inner flow flagged", func(t *testing.T) {
		sp := noneStartSP(t, "dangling")

		// removing an endpoint leaves its flows dangling — Validate must
		// surface the endpoint check alongside the shape rules.
		var end flow.Node
		for _, n := range sp.Nodes() {
			if en, ok := n.(flow.EventNode); ok &&
				en.EventClass() == flow.EndEventClass {
				end = n
			}
		}
		require.NotNil(t, end)
		require.NoError(t, sp.Remove(end))

		err := sp.Validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "is not in the container")
	})

	t.Run("boundary hosted outside rejected", func(t *testing.T) {
		sp, err := activities.NewSubProcess("foreign-host")
		require.NoError(t, err)

		require.NoError(t, sp.Add(spTask(t, "entry")))

		outside := spTask(t, "outside-host")

		sig, err := events.NewSignal("f-sig",
			data.MustItemDefinition(values.NewVariable(1)))
		require.NoError(t, err)
		sdef, err := events.NewSignalEventDefinition(sig)
		require.NoError(t, err)

		be, err := events.NewBoundaryEvent("f-bnd", outside, sdef, true)
		require.NoError(t, err)
		require.NoError(t, sp.Add(be))

		err = sp.Validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "outside the Sub-Process")
	})
}
