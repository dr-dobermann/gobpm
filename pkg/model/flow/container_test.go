package flow_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/stretchr/testify/require"
)

// lyingElement claims NodeElement but is not a flow.Node — the AddElement
// type-dispatch defensive branch.
type lyingElement struct {
	foundation.BaseElement
}

func (l *lyingElement) Name() string                  { return "liar" }
func (l *lyingElement) Container() flow.Container     { return nil }
func (l *lyingElement) EType() flow.ElementType       { return flow.NodeElement }
func (l *lyingElement) BindTo(_ flow.Container) error { return nil }
func (l *lyingElement) Unbind() error                 { return nil }

// oddElement reports an element type the dispatch doesn't accept.
type oddElement struct{ lyingElement }

func (o *oddElement) EType() flow.ElementType { return flow.DataObjectElement }

// lyingFlow claims SequenceBaseElement but is not a *flow.SequenceFlow.
type lyingFlow struct{ lyingElement }

func (l *lyingFlow) EType() flow.ElementType { return flow.SequenceBaseElement }

// stubOwner is a minimal Container: identity from BaseElement, the graph
// from the embedded core — the composition shape SubProcess/Process use.
type stubOwner struct {
	flow.ElementsContainer
	foundation.BaseElement
}

func newStubOwner(t *testing.T) *stubOwner {
	t.Helper()

	be, err := foundation.NewBaseElement()
	require.NoError(t, err)

	return &stubOwner{
		ElementsContainer: flow.NewElementsContainer(),
		BaseElement:       *be,
	}
}

// Add binds elements to the owner through the core.
func (o *stubOwner) Add(e flow.Element) error { return o.AddElement(o, e) }

// Remove drops elements from the core.
func (o *stubOwner) Remove(e flow.Element) error { return o.RemoveElement(e) }

// node builds a plain None start/end event as a generic flow.Node.
func node(t *testing.T, name string, start bool) flow.Node {
	t.Helper()

	if start {
		n, err := events.NewStartEvent(name)
		require.NoError(t, err)

		return n
	}

	n, err := events.NewEndEvent(name)
	require.NoError(t, err)

	return n
}

func TestElementsContainer(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	t.Run("nil owner and nil element rejected", func(t *testing.T) {
		o := newStubOwner(t)
		n := node(t, "n", true)

		require.Error(t, o.AddElement(nil, n))
		require.Error(t, o.Add(nil))
		require.Error(t, o.Remove(nil))
	})

	t.Run("add nodes and flows, accessors", func(t *testing.T) {
		o := newStubOwner(t)
		s, e := node(t, "s", true), node(t, "e", false)

		require.NoError(t, o.Add(s))
		require.NoError(t, o.Add(e))

		f, err := flow.Link(s.(flow.SequenceSource), e.(flow.SequenceTarget))
		require.NoError(t, err)
		// Link auto-adds the flow into the source's container.
		require.Len(t, o.Flows(), 1)
		require.Equal(t, f.ID(), o.Flows()[0].ID())

		require.Len(t, o.Nodes(), 2)
		require.Len(t, o.Elements(), 3)

		require.NoError(t, o.ValidateFlows())
	})

	t.Run("duplicates rejected", func(t *testing.T) {
		o := newStubOwner(t)
		s := node(t, "dup", true)

		require.NoError(t, o.Add(s))
		require.ErrorContains(t, o.Add(s), "already in the container")
	})

	t.Run("remove node and flow, missing rejected", func(t *testing.T) {
		o := newStubOwner(t)
		s, e := node(t, "s", true), node(t, "e", false)

		require.NoError(t, o.Add(s))
		require.NoError(t, o.Add(e))

		f, err := flow.Link(s.(flow.SequenceSource), e.(flow.SequenceTarget))
		require.NoError(t, err)

		require.NoError(t, o.Remove(f))
		require.Empty(t, o.Flows())

		require.NoError(t, o.Remove(s))
		require.ErrorContains(t, o.Remove(s), "isn't in the container")
	})

	t.Run("endpoint validation flags foreign endpoints", func(t *testing.T) {
		o := newStubOwner(t)
		s, e := node(t, "s", true), node(t, "e", false)

		require.NoError(t, o.Add(s))
		require.NoError(t, o.Add(e))

		f, err := flow.Link(s.(flow.SequenceSource), e.(flow.SequenceTarget))
		require.NoError(t, err)
		require.NotNil(t, f)

		// removing an endpoint node leaves the flow dangling — the
		// per-container endpoint check must flag both directions.
		require.NoError(t, o.Remove(s))
		require.NoError(t, o.Remove(e))

		err = o.ValidateFlows()
		require.Error(t, err)
		require.Contains(t, err.Error(), "source")
		require.Contains(t, err.Error(), "target")
	})
}

func TestElementsContainerDefensive(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	t.Run("lying node type rejected", func(t *testing.T) {
		o := newStubOwner(t)
		err := o.Add(&lyingElement{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "not a Node")
	})

	t.Run("lying flow type rejected", func(t *testing.T) {
		o := newStubOwner(t)
		err := o.Add(&lyingFlow{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "not a *SequenceFlow")
	})

	t.Run("unsupported element type rejected", func(t *testing.T) {
		o := newStubOwner(t)
		err := o.Add(&oddElement{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid flow element type")
	})

	t.Run("node bound elsewhere rejected", func(t *testing.T) {
		o1, o2 := newStubOwner(t), newStubOwner(t)
		n := node(t, "shared", true)

		require.NoError(t, o1.Add(n))
		require.Error(t, o2.Add(n), "BindTo rejects a second container")
	})

	t.Run("duplicate and foreign flow rejected", func(t *testing.T) {
		o := newStubOwner(t)
		s, e := node(t, "s", true), node(t, "e", false)
		require.NoError(t, o.Add(s))
		require.NoError(t, o.Add(e))

		f, err := flow.Link(s.(flow.SequenceSource), e.(flow.SequenceTarget))
		require.NoError(t, err)

		// Link already auto-added the flow — a second Add is a duplicate.
		require.ErrorContains(t, o.Add(f), "already in the container")

		// a flow bound to o cannot be added to another owner.
		o2 := newStubOwner(t)
		require.Error(t, o2.Add(f))
	})
}

// TestCloneGraph — the shared wiring in its home package: relink, default
// remap, boundary rebind, node-clone error propagation.
func TestCloneGraph(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	t.Run("relink and rebind", func(t *testing.T) {
		sp, err := activities.NewSubProcess("wiring")
		require.NoError(t, err)

		s, e := node(t, "s", true), node(t, "e", false)
		require.NoError(t, sp.Add(s))
		require.NoError(t, sp.Add(e))

		f, err := flow.Link(s.(flow.SequenceSource), e.(flow.SequenceTarget))
		require.NoError(t, err)
		require.NotNil(t, f)

		core := sp.ElementsContainer
		cloned, err := core.CloneGraph()
		require.NoError(t, err)

		require.Len(t, cloned.Nodes(), 2)
		require.Len(t, cloned.Flows(), 1)

		cf := cloned.Flows()[0]
		require.Equal(t, f.ID(), cf.ID())
		require.NotSame(t, f, cf)
		require.NotSame(t, s, cf.Source().Node(),
			"the cloned edge must reference the CLONED source")
	})

	t.Run("boundary rebind through the helper", func(t *testing.T) {
		sp, err := activities.NewSubProcess("wiring-bnd")
		require.NoError(t, err)

		host, err := activities.NewSubProcess("host") // any activity works
		require.NoError(t, err)
		inner, err := events.NewStartEvent("h-start")
		require.NoError(t, err)
		require.NoError(t, host.Add(inner))
		hEnd, err := events.NewEndEvent("h-end")
		require.NoError(t, err)
		require.NoError(t, host.Add(hEnd))
		_, err = flow.Link(inner, hEnd)
		require.NoError(t, err)

		require.NoError(t, sp.Add(host))

		sig, err := events.NewSignal("w-sig",
			data.MustItemDefinition(values.NewVariable(1)))
		require.NoError(t, err)
		sdef, err := events.NewSignalEventDefinition(sig)
		require.NoError(t, err)

		be, err := events.NewBoundaryEvent("w-bnd", host, sdef, true)
		require.NoError(t, err)
		require.NoError(t, sp.Add(be))

		cloned, err := sp.ElementsContainer.CloneGraph()
		require.NoError(t, err)

		var cbe flow.BoundaryEvent
		var chost flow.ActivityNode
		for _, n := range cloned.Nodes() {
			if b, ok := n.(flow.BoundaryEvent); ok {
				cbe = b
			} else if a, ok := n.(flow.ActivityNode); ok {
				chost = a
			}
		}
		require.NotNil(t, cbe)
		require.NotNil(t, chost)
		require.Same(t, chost, cbe.AttachedTo())
	})

	t.Run("default flow remapped", func(t *testing.T) {
		sp, err := activities.NewSubProcess("wiring-df")
		require.NoError(t, err)

		gw, err := gateways.NewExclusiveGateway()
		require.NoError(t, err)
		e1 := node(t, "e1", false)
		e2 := node(t, "e2", false)

		for _, el := range []flow.Element{gw, e1, e2} {
			require.NoError(t, sp.Add(el))
		}

		_, err = flow.Link(gw, e1.(flow.SequenceTarget))
		require.NoError(t, err)
		df, err := flow.Link(gw, e2.(flow.SequenceTarget))
		require.NoError(t, err)
		require.NoError(t, gw.UpdateDefaultFlow(df))

		cloned, err := sp.ElementsContainer.CloneGraph()
		require.NoError(t, err)

		var ch flow.DefaultFlowHolder
		for _, n := range cloned.Nodes() {
			if h, ok := n.(flow.DefaultFlowHolder); ok && h.DefaultFlow() != nil {
				ch = h
			}
		}
		require.NotNil(t, ch)
		require.Equal(t, df.ID(), ch.DefaultFlow().ID())
		require.NotSame(t, df, ch.DefaultFlow())
	})

	t.Run("node clone failure propagates", func(t *testing.T) {
		sp, err := activities.NewSubProcess("wiring-bad")
		require.NoError(t, err)

		// a value-less property (a bare zero struct — FIX-017/018) makes an
		// inner node's Clone fail; the wrap must surface it.
		bad, err := activities.NewSubProcess("bad-inner",
			data.WithProperties(&data.Property{}))
		require.NoError(t, err)
		require.NoError(t, sp.Add(bad))

		_, err = sp.ElementsContainer.CloneGraph()
		require.Error(t, err)
		require.Contains(t, err.Error(), "couldn't clone node")

		// an empty core clones to an empty core.
		empty, err := activities.NewSubProcess("empty-core")
		require.NoError(t, err)
		cloned, err := empty.ElementsContainer.CloneGraph()
		require.NoError(t, err)
		require.Empty(t, cloned.Nodes())
	})
}

// TestWireClonedGraphDefensive — the corrupt-clone branches, forced by
// calling the exported helper directly with crafted maps: a "clone" that
// lost the SequenceSource/Target capability, and a boundary rebind onto a
// host that already carries the binding (the multiplicity conflict).
func TestWireClonedGraphDefensive(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	// a valid s→e edge as the wiring source.
	build := func(t *testing.T) (flow.Node, flow.Node, *flow.SequenceFlow) {
		t.Helper()

		o := newStubOwner(t)
		s, e := node(t, "s", true), node(t, "e", false)
		require.NoError(t, o.Add(s))
		require.NoError(t, o.Add(e))

		f, err := flow.Link(s.(flow.SequenceSource), e.(flow.SequenceTarget))
		require.NoError(t, err)

		return s, e, f
	}

	t.Run("cloned source lost the capability", func(t *testing.T) {
		s, e, f := build(t)

		// an EndEvent supports no outgoing flows — as the "clone" of the
		// source it fails the SequenceSource cast.
		notASource := node(t, "fake", false)

		_, err := flow.WireClonedGraph(
			map[string]flow.Node{s.ID(): notASource, e.ID(): e},
			map[string]flow.Node{s.ID(): s, e.ID(): e},
			map[string]*flow.SequenceFlow{f.ID(): f})
		require.Error(t, err)
		require.Contains(t, err.Error(), "isn't a sequence source")
	})

	t.Run("cloned target lost the capability", func(t *testing.T) {
		s, e, f := build(t)

		// a StartEvent accepts no incoming flows — as the "clone" of the
		// target it fails the SequenceTarget cast.
		notATarget := node(t, "fake", true)

		_, err := flow.WireClonedGraph(
			map[string]flow.Node{s.ID(): s, e.ID(): notATarget},
			map[string]flow.Node{s.ID(): s, e.ID(): e},
			map[string]*flow.SequenceFlow{f.ID(): f})
		require.Error(t, err)
		require.Contains(t, err.Error(), "isn't a sequence target")
	})

	t.Run("boundary rebind conflict surfaces", func(t *testing.T) {
		host, err := activities.NewSubProcess("bh")
		require.NoError(t, err)
		require.NoError(t, host.Add(node(t, "hs", true)))

		sig, err := events.NewSignal("d-sig",
			data.MustItemDefinition(values.NewVariable(1)))
		require.NoError(t, err)
		sdef, err := events.NewSignalEventDefinition(sig)
		require.NoError(t, err)

		be, err := events.NewBoundaryEvent("d-bnd", host, sdef, true)
		require.NoError(t, err)

		// passing the ORIGINALS as "clones" re-attaches the boundary onto a
		// host that already carries it — the multiplicity conflict the
		// rebind wrap must surface.
		nodes := map[string]flow.Node{host.ID(): host, be.ID(): be}

		_, err = flow.WireClonedGraph(nodes, nodes,
			map[string]*flow.SequenceFlow{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "rebind boundary")
	})
}
