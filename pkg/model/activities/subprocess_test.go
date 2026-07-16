package activities_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
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

}

// nodeByName finds an inner node by name.
func nodeByName(t *testing.T, sp *activities.SubProcess, name string) flow.Node {
	t.Helper()

	for _, n := range sp.Nodes() {
		if n.Name() == name {
			return n
		}
	}

	t.Fatalf("inner node %q not found", name)

	return nil
}

// TestSubProcessClone — the FR-4 deep clone: disjoint inner graphs, flow
// endpoints on the CLONED nodes, the inner gateway default remapped, the
// inner boundary rebound to its cloned host, and a nested sub-process
// disjoint too (recursion). The Clone-drop bug class pin.
func TestSubProcessClone(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	// outer: start → task(+boundary→exc) → gw{cond→endA, default→endB},
	// plus a nested inner sub-process on the gw's conditional path target.
	sp, err := activities.NewSubProcess("clone-src")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	task := spTask(t, "guarded")
	gw, err := gateways.NewExclusiveGateway()
	require.NoError(t, err)
	nested := noneStartSP(t, "nested")
	endB, err := events.NewEndEvent("endB")
	require.NoError(t, err)
	exc, err := events.NewEndEvent("exc")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, task, gw, nested, endB, exc} {
		require.NoError(t, sp.Add(e))
	}

	// the boundary on the inner task.
	sig, err := events.NewSignal("cl-sig",
		data.MustItemDefinition(values.NewVariable(1)))
	require.NoError(t, err)
	sdef, err := events.NewSignalEventDefinition(sig)
	require.NoError(t, err)
	be, err := events.NewBoundaryEvent("bnd", task, sdef, true)
	require.NoError(t, err)
	require.NoError(t, sp.Add(be))
	_, err = flow.Link(be, exc)
	require.NoError(t, err)

	_, err = flow.Link(start, task)
	require.NoError(t, err)
	_, err = flow.Link(task, gw)
	require.NoError(t, err)

	cond := goexprBool(t)
	_, err = flow.Link(gw, nested, flow.WithCondition(cond))
	require.NoError(t, err)

	df, err := flow.Link(gw, endB)
	require.NoError(t, err)
	require.NoError(t, gw.UpdateDefaultFlow(df))

	require.NoError(t, sp.Validate())

	cn, err := sp.Clone()
	require.NoError(t, err)

	csp, ok := cn.(*activities.SubProcess)
	require.True(t, ok)

	t.Run("disjoint inner nodes and flows", func(t *testing.T) {
		require.Len(t, csp.Nodes(), len(sp.Nodes()))
		require.Len(t, csp.Flows(), len(sp.Flows()))

		src := map[flow.Node]bool{}
		for _, n := range sp.Nodes() {
			src[n] = true
		}
		for _, n := range csp.Nodes() {
			require.False(t, src[n], "cloned node %q is shared", n.Name())
		}
	})

	t.Run("flow endpoints reference cloned nodes", func(t *testing.T) {
		ct := nodeByName(t, csp, "guarded")
		found := false
		for _, f := range csp.Flows() {
			if f.Target().ID() == ct.ID() {
				require.Same(t, ct, f.Target().Node())
				found = true
			}
		}
		require.True(t, found)
	})

	t.Run("default flow remapped", func(t *testing.T) {
		var cgw flow.DefaultFlowHolder
		for _, n := range csp.Nodes() {
			if h, ok := n.(flow.DefaultFlowHolder); ok && h.DefaultFlow() != nil {
				cgw = h
			}
		}
		require.NotNil(t, cgw)
		require.Equal(t, df.ID(), cgw.DefaultFlow().ID())
		require.NotSame(t, df, cgw.DefaultFlow(),
			"the default must point at the CLONED edge")
	})

	t.Run("boundary rebound to cloned host", func(t *testing.T) {
		cbe := nodeByName(t, csp, "bnd").(flow.BoundaryEvent)
		chost := nodeByName(t, csp, "guarded").(flow.ActivityNode)
		require.Same(t, chost, cbe.AttachedTo())

		bes := chost.(interface{ BoundaryEvents() []flow.EventNode }).BoundaryEvents()
		require.Len(t, bes, 1)
		require.Same(t, cbe, bes[0])
	})

	t.Run("nested sub-process disjoint", func(t *testing.T) {
		cNested := nodeByName(t, csp, "nested").(*activities.SubProcess)
		srcNested := nodeByName(t, sp, "nested").(*activities.SubProcess)
		require.NotSame(t, srcNested, cNested)

		srcInner := map[flow.Node]bool{}
		for _, n := range srcNested.Nodes() {
			srcInner[n] = true
		}
		require.NotEmpty(t, srcInner)
		for _, n := range cNested.Nodes() {
			require.False(t, srcInner[n],
				"nested inner node %q is shared", n.Name())
		}
	})

	t.Run("two clones disjoint from each other", func(t *testing.T) {
		cn2, err := sp.Clone()
		require.NoError(t, err)
		csp2 := cn2.(*activities.SubProcess)

		first := map[flow.Node]bool{}
		for _, n := range csp.Nodes() {
			first[n] = true
		}
		for _, n := range csp2.Nodes() {
			require.False(t, first[n])
		}
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

// TestSubProcessCloneFailures — the error-wrap branches: a value-less
// property (the only remaining value-less source, a bare zero struct —
// FIX-017/018) breaks the activity-config clone; the same on an INNER node
// breaks the inner-graph clone.
func TestSubProcessCloneFailures(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	t.Run("activity clone failure propagates", func(t *testing.T) {
		sp, err := activities.NewSubProcess("bad-own-prop",
			data.WithProperties(&data.Property{}))
		require.NoError(t, err)
		require.NoError(t, sp.Add(spTask(t, "entry")))

		_, err = sp.Clone()
		require.Error(t, err)
		require.Contains(t, err.Error(), "couldn't clone sub-process")
	})

	t.Run("inner clone failure propagates", func(t *testing.T) {
		sp, err := activities.NewSubProcess("bad-inner")
		require.NoError(t, err)

		op, err := gooper.New("bad-op",
			func(_ context.Context, _ service.DataReader,
				_ *data.ItemDefinition) (*data.ItemDefinition, error) {
				return nil, nil
			})
		require.NoError(t, err)

		bad, err := activities.NewServiceTask("bad-task", op,
			activities.WithoutParams(), data.WithProperties(&data.Property{}))
		require.NoError(t, err)
		require.NoError(t, sp.Add(bad))

		_, err = sp.Clone()
		require.Error(t, err)
		require.Contains(t, err.Error(), "inner graph")
	})
}

// goexprBool builds a constant-true bool condition for gateway edges.
func goexprBool(t *testing.T) data.FormalExpression {
	t.Helper()

	c, err := goexpr.New(nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(_ context.Context, _ data.Source) (data.Value, error) {
			return values.NewVariable(true), nil
		})
	require.NoError(t, err)

	return c
}

// TestSubProcessRuntimeSurface — the execution-facing methods in their home
// package: ProcessEvent accepts the completion (nil rejected), Exec selects
// the outgoing flows, Node returns the concrete type.
func TestSubProcessRuntimeSurface(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	sp := noneStartSP(t, "surface")

	t.Run("Node returns the SubProcess itself", func(t *testing.T) {
		require.Same(t, sp, sp.Node())
	})

	t.Run("ProcessEvent accepts a completion, rejects nil", func(t *testing.T) {
		require.Error(t, sp.ProcessEvent(t.Context(), nil))

		sig, err := events.NewSignal("sd",
			data.MustItemDefinition(values.NewVariable(1)))
		require.NoError(t, err)
		sdef, err := events.NewSignalEventDefinition(sig)
		require.NoError(t, err)
		require.NoError(t, sp.ProcessEvent(t.Context(), sdef))
	})

	t.Run("Exec selects the single outgoing", func(t *testing.T) {
		// give the composite one outgoing inside a wrapper container: the
		// single-flow short-circuit needs no runtime environment.
		owner, err := activities.NewSubProcess("wrapper")
		require.NoError(t, err)

		inner := noneStartSP(t, "exec-inner")
		next := spTask(t, "next")
		require.NoError(t, owner.Add(inner))
		require.NoError(t, owner.Add(next))

		f, err := flow.Link(inner, next)
		require.NoError(t, err)

		out, err := inner.Exec(t.Context(), nil)
		require.NoError(t, err)
		require.Len(t, out, 1)
		require.Equal(t, f.ID(), out[0].ID())
	})
}
