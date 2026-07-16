package flow_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
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
