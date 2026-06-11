package flow_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/require"
)

// TestCloneFlow verifies the flow.CloneFlow edge helper: it preserves the
// original flow id and condition, wires both cloned endpoints and performs no
// container insertion.
func TestCloneFlow(t *testing.T) {
	cond, err := goexpr.New(
		nil,
		data.MustItemDefinition(values.NewVariable(true)),
		func(_ context.Context, _ data.Source) (data.Value, error) {
			return values.NewVariable(true), nil
		})
	require.NoError(t, err)

	t.Run("nil arguments rejected",
		func(t *testing.T) {
			se, err := events.NewStartEvent("start")
			require.NoError(t, err)
			ee, err := events.NewEndEvent("end")
			require.NoError(t, err)
			orig, err := flow.Link(se, ee)
			require.NoError(t, err)

			_, err = flow.CloneFlow(nil, se, ee)
			require.Error(t, err)

			_, err = flow.CloneFlow(orig, nil, ee)
			require.Error(t, err)

			_, err = flow.CloneFlow(orig, se, nil)
			require.Error(t, err)
		})

	t.Run("id and condition preserved, both endpoints wired",
		func(t *testing.T) {
			se, err := events.NewStartEvent("start")
			require.NoError(t, err)
			ee, err := events.NewEndEvent("end")
			require.NoError(t, err)

			orig, err := flow.Link(se, ee,
				foundation.WithID("edge-1"),
				flow.WithCondition(cond))
			require.NoError(t, err)

			seClone, ok := se.Clone().(*events.StartEvent)
			require.True(t, ok)
			eeClone, ok := ee.Clone().(*events.EndEvent)
			require.True(t, ok)

			require.Empty(t, seClone.Outgoing())
			require.Empty(t, eeClone.Incoming())

			cf, err := flow.CloneFlow(orig, seClone, eeClone)
			require.NoError(t, err)

			// id preserved.
			require.Equal(t, orig.ID(), cf.ID())
			require.Equal(t, "edge-1", cf.ID())

			// condition preserved by reference.
			require.Same(t, cond, cf.Condition())

			// both endpoints wired to the clones.
			require.Same(t, seClone, cf.Source())
			require.Same(t, eeClone, cf.Target())

			require.Len(t, seClone.Outgoing(), 1)
			require.Same(t, cf, seClone.Outgoing()[0])
			require.Len(t, eeClone.Incoming(), 1)
			require.Same(t, cf, eeClone.Incoming()[0])

			// no container insertion happened on the clones.
			require.Nil(t, cf.Container())
			require.Nil(t, seClone.Container())
			require.Nil(t, eeClone.Container())
		})
}

// TestMustCloneFlow verifies the panicking wrapper around CloneFlow: it returns
// the cloned edge on valid input and panics when CloneFlow would error.
func TestMustCloneFlow(t *testing.T) {
	se, err := events.NewStartEvent("start")
	require.NoError(t, err)
	ee, err := events.NewEndEvent("end")
	require.NoError(t, err)

	orig, err := flow.Link(se, ee, foundation.WithID("edge-1"))
	require.NoError(t, err)

	seClone, ok := se.Clone().(*events.StartEvent)
	require.True(t, ok)
	eeClone, ok := ee.Clone().(*events.EndEvent)
	require.True(t, ok)

	t.Run("success returns cloned edge",
		func(t *testing.T) {
			cf := flow.MustCloneFlow(orig, seClone, eeClone)
			require.NotNil(t, cf)
			require.Equal(t, orig.ID(), cf.ID())
			require.Same(t, seClone, cf.Source())
			require.Same(t, eeClone, cf.Target())
		})

	t.Run("nil original panics",
		func(t *testing.T) {
			require.Panics(t, func() {
				_ = flow.MustCloneFlow(nil, seClone, eeClone)
			})
		})
}

func TestBaseNodeCloneStub(t *testing.T) {
	bn, err := flow.NewBaseNode("bn")
	require.NoError(t, err)

	// The generic BaseNode has no standalone identity as a graph node: each
	// concrete node type implements Clone; calling it on the bare base panics.
	require.Panics(t, func() { _ = bn.Clone() })
}
