package lanes_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/lanes"
	"github.com/stretchr/testify/require"
)

// node builds a real flow.Node with the given id. A lane only ever reads a
// node's identity, never its behavior, so the cheapest real node will do — and
// using a real one keeps the test honest about what Place actually stores.
func node(t *testing.T, id string) flow.Node {
	t.Helper()

	se, err := events.NewStartEvent(id, foundation.WithID(id))
	require.NoError(t, err)

	return se
}

// partitionA and partitionB are two DIFFERENT foundation.Identifyer
// implementations, so Table 10.135's same-type rule has something to compare.
type partitionA struct{ id string }

func (p partitionA) ID() string { return p.id }

type partitionB struct{ id string }

func (p partitionB) ID() string { return p.id }

// TestNewLaneSet — SRD-076 T-1: construction, optional name, declaration order.
func TestNewLaneSet(t *testing.T) {
	t.Run("named set keeps its lanes in order", func(t *testing.T) {
		first, err := lanes.NewLane("first", nil, "", nil)
		require.NoError(t, err)

		second, err := lanes.NewLane("second", nil, "", nil)
		require.NoError(t, err)

		ls, err := lanes.NewLaneSet("sales", []*lanes.Lane{first, second})
		require.NoError(t, err)

		require.Equal(t, "sales", ls.Name())
		require.Len(t, ls.Lanes(), 2)
		require.Equal(t, "first", ls.Lanes()[0].Name())
		require.Equal(t, "second", ls.Lanes()[1].Name())
	})

	t.Run("an unnamed set is accepted (cardinality 0..1)",
		func(t *testing.T) {
			ls, err := lanes.NewLaneSet("", nil)
			require.NoError(t, err)
			require.Empty(t, ls.Name())
			require.Empty(t, ls.Lanes())
		})

	t.Run("a nil lane is refused", func(t *testing.T) {
		_, err := lanes.NewLaneSet("sales", []*lanes.Lane{nil})
		require.Error(t, err)
	})

	t.Run("an invalid base option is propagated", func(t *testing.T) {
		_, err := lanes.NewLaneSet("sales", nil, foundation.WithID("  "))
		require.Error(t, err)
	})

	t.Run("Lanes returns a copy, not an alias", func(t *testing.T) {
		l, err := lanes.NewLane("only", nil, "", nil)
		require.NoError(t, err)

		ls, err := lanes.NewLaneSet("set", []*lanes.Lane{l})
		require.NoError(t, err)

		got := ls.Lanes()
		got[0] = nil

		require.NotNil(t, ls.Lanes()[0])
	})
}

// TestLaneSetPartitionTypes — SRD-076 T-6 at construction: Table 10.135's
// same-type MUST is visible in the lane set itself, so it is enforced where it
// is written rather than deferred to registration.
func TestLaneSetPartitionTypes(t *testing.T) {
	t.Run("same partition type is accepted", func(t *testing.T) {
		a1, err := lanes.NewLane("a1", partitionA{id: "r1"}, "", nil)
		require.NoError(t, err)

		a2, err := lanes.NewLane("a2", partitionA{id: "r2"}, "", nil)
		require.NoError(t, err)

		_, err = lanes.NewLaneSet("set", []*lanes.Lane{a1, a2})
		require.NoError(t, err,
			"different instances of one type are exactly what the spec allows")
	})

	t.Run("mixed partition types are refused", func(t *testing.T) {
		a, err := lanes.NewLane("a", partitionA{id: "r1"}, "", nil)
		require.NoError(t, err)

		b, err := lanes.NewLane("b", partitionB{id: "r2"}, "", nil)
		require.NoError(t, err)

		_, err = lanes.NewLaneSet("set", []*lanes.Lane{a, b})
		require.Error(t, err)
		require.ErrorContains(t, err, "mixes partition element types")
	})

	t.Run("lanes declaring no partition element are exempt",
		func(t *testing.T) {
			a, err := lanes.NewLane("a", partitionA{id: "r1"}, "", nil)
			require.NoError(t, err)

			plain, err := lanes.NewLane("plain", nil, "", nil)
			require.NoError(t, err)

			_, err = lanes.NewLaneSet("set", []*lanes.Lane{a, plain})
			require.NoError(t, err,
				"partitionElement is 0..1, so declaring none conflicts with nothing")
		})
}

// TestNewLane — SRD-076 T-2: an unnamed lane is legal, and every optional
// property may be omitted.
func TestNewLane(t *testing.T) {
	t.Run("a plain named lane needs nothing else", func(t *testing.T) {
		l, err := lanes.NewLane("sales", nil, "", nil)
		require.NoError(t, err)

		require.Equal(t, "sales", l.Name())
		require.Nil(t, l.PartitionElement())
		require.Empty(t, l.PartitionElementRef())
		require.Nil(t, l.ChildLaneSet())
		require.Empty(t, l.FlowNodes())
	})

	t.Run("an unnamed lane is accepted (cardinality 0..1)",
		func(t *testing.T) {
			l, err := lanes.NewLane("", nil, "", nil)
			require.NoError(t, err)
			require.Empty(t, l.Name())
		})

	t.Run("an invalid base option is propagated", func(t *testing.T) {
		_, err := lanes.NewLane("sales", nil, "", nil, foundation.WithID(" "))
		require.Error(t, err)
	})
}

// TestLaneAccessors — SRD-076 T-3: every carried property reads back, and slice
// accessors hand out copies.
func TestLaneAccessors(t *testing.T) {
	child, err := lanes.NewLaneSet("child", nil)
	require.NoError(t, err)

	pe := partitionA{id: "resource-1"}

	l, err := lanes.NewLane("sales", pe, "  ref-1  ", child,
		foundation.WithID("lane-1"))
	require.NoError(t, err)

	require.Equal(t, "sales", l.Name())
	require.Equal(t, "lane-1", l.ID())
	require.Equal(t, pe, l.PartitionElement())
	require.Equal(t, "ref-1", l.PartitionElementRef(), "trimmed")
	require.Same(t, child, l.ChildLaneSet())

	require.NoError(t, l.Place(node(t, "n1")))

	got := l.FlowNodes()
	require.Len(t, got, 1)

	got[0] = nil
	require.NotNil(t, l.FlowNodes()[0], "FlowNodes must hand out a copy")
}

// TestLanePlace — SRD-076 T-3a: placement runs FROM the lane, is variadic so a
// group goes on in one call, accumulates across calls, and is safe to repeat.
func TestLanePlace(t *testing.T) {
	t.Run("a single element", func(t *testing.T) {
		l, err := lanes.NewLane("sales", nil, "", nil)
		require.NoError(t, err)

		require.NoError(t, l.Place(node(t, "n1")))
		require.Len(t, l.FlowNodes(), 1)
	})

	t.Run("a group in one call", func(t *testing.T) {
		l, err := lanes.NewLane("sales", nil, "", nil)
		require.NoError(t, err)

		require.NoError(t,
			l.Place(node(t, "n1"), node(t, "n2"), node(t, "n3")))
		require.Len(t, l.FlowNodes(), 3)
	})

	t.Run("repeated calls accumulate", func(t *testing.T) {
		l, err := lanes.NewLane("sales", nil, "", nil)
		require.NoError(t, err)

		require.NoError(t, l.Place(node(t, "n1")))
		require.NoError(t, l.Place(node(t, "n2")))
		require.Len(t, l.FlowNodes(), 2)
	})

	t.Run("re-placing the same node is a no-op", func(t *testing.T) {
		l, err := lanes.NewLane("sales", nil, "", nil)
		require.NoError(t, err)

		require.NoError(t, l.Place(node(t, "n1"), node(t, "n2")))
		require.NoError(t, l.Place(node(t, "n1")))
		require.Len(t, l.FlowNodes(), 2,
			"placing a group twice must not duplicate its members")
	})

	t.Run("a nil node is refused", func(t *testing.T) {
		l, err := lanes.NewLane("sales", nil, "", nil)
		require.NoError(t, err)

		require.Error(t, l.Place(nil))
		require.Empty(t, l.FlowNodes())
	})

	t.Run("a nil among valid nodes refuses the whole call",
		func(t *testing.T) {
			l, err := lanes.NewLane("sales", nil, "", nil)
			require.NoError(t, err)

			require.Error(t, l.Place(node(t, "n1"), nil))
			require.Empty(t, l.FlowNodes(),
				"placement is all-or-nothing, so a bad call leaves no residue")
		})
}

// TestLaneNesting — SRD-076 T-4: a child lane set round-trips to depth.
func TestLaneNesting(t *testing.T) {
	inner, err := lanes.NewLane("inner", nil, "", nil)
	require.NoError(t, err)

	innerSet, err := lanes.NewLaneSet("inner-set", []*lanes.Lane{inner})
	require.NoError(t, err)

	middle, err := lanes.NewLane("middle", nil, "", innerSet)
	require.NoError(t, err)

	middleSet, err := lanes.NewLaneSet("middle-set", []*lanes.Lane{middle})
	require.NoError(t, err)

	outer, err := lanes.NewLane("outer", nil, "", middleSet)
	require.NoError(t, err)

	require.Equal(t, "middle-set", outer.ChildLaneSet().Name())
	require.Equal(t, "middle", outer.ChildLaneSet().Lanes()[0].Name())
	require.Equal(t, "inner-set",
		outer.ChildLaneSet().Lanes()[0].ChildLaneSet().Name())
	require.Equal(t, "inner",
		outer.ChildLaneSet().Lanes()[0].ChildLaneSet().Lanes()[0].Name())
}

// TestMustConstructors covers the fixture twins on both paths.
func TestMustConstructors(t *testing.T) {
	require.NotNil(t, lanes.MustLane("sales", nil, "", nil))
	require.NotNil(t, lanes.MustLaneSet("set", nil))

	require.Panics(t, func() {
		lanes.MustLane("sales", nil, "", nil, foundation.WithID(" "))
	})

	require.Panics(t, func() {
		lanes.MustLaneSet("set", []*lanes.Lane{nil})
	})
}
