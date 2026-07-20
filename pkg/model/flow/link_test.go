package flow_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/stretchr/testify/require"
)

func lThrow(t *testing.T, id, name string) *events.IntermediateThrowEvent {
	t.Helper()

	e, err := events.NewIntermediateThrowEvent(
		id, events.MustLinkEventDefinition(name))
	require.NoError(t, err)

	return e
}

func lCatch(t *testing.T, id, name string) *events.IntermediateCatchEvent {
	t.Helper()

	e, err := events.NewIntermediateCatchEvent(
		id, events.MustLinkEventDefinition(name))
	require.NoError(t, err)

	return e
}

// wireClones clones every node and wires the clone set (the real snapshot /
// CloneGraph path — resolveLinkEdges runs as WireClonedGraph's last step),
// returning the cloned node set.
func wireClones(
	t *testing.T, src map[string]flow.Node, srcFlows map[string]*flow.SequenceFlow,
) map[string]flow.Node {
	t.Helper()

	clones := make(map[string]flow.Node, len(src))
	for id, n := range src {
		cn, err := n.Clone()
		require.NoError(t, err)

		clones[id] = cn
	}

	_, err := flow.WireClonedGraph(clones, src, srcFlows)
	require.NoError(t, err)

	return clones
}

// TestResolveLinkEdges covers the Link resolution step of WireClonedGraph
// (SRD-057 M2, FR-4): a throw is paired to its same-name catch, many sources
// pair to one target, and an unpaired throw is left unresolved.
func TestResolveLinkEdges(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()

	// end gives a catch a downstream flow, so a resolved throw redirect is
	// observable as a non-empty Outgoing().
	newLinked := func(t *testing.T, names ...string) (
		map[string]flow.Node, map[string]*flow.SequenceFlow, string,
	) {
		t.Helper()

		cat := lCatch(t, "cat", "L")
		end, err := events.NewEndEvent("end")
		require.NoError(t, err)

		f, err := flow.Link(cat, end)
		require.NoError(t, err)

		src := map[string]flow.Node{cat.ID(): cat, end.ID(): end}
		for i, name := range names {
			thr := lThrow(t, "thr"+string(rune('0'+i)), name)
			src[thr.ID()] = thr
		}

		return src, map[string]*flow.SequenceFlow{f.ID(): f}, cat.ID()
	}

	redirect := func(t *testing.T, clones map[string]flow.Node, throwID string) ([]*flow.SequenceFlow, error) {
		t.Helper()

		return clones[throwID].(*events.IntermediateThrowEvent).Exec(ctx, nil)
	}

	t.Run("a throw pairs to its same-name catch", func(t *testing.T) {
		src, flows, _ := newLinked(t, "L")
		var throwID string
		for id, n := range src {
			if ln, ok := n.(flow.LinkEventNode); ok && ln.IsLinkSource() {
				throwID = id
			}
		}

		clones := wireClones(t, src, flows)

		got, err := redirect(t, clones, throwID)
		require.NoError(t, err)
		require.Len(t, got, 1) // the catch's rewired downstream
	})

	t.Run("many sources pair to one target", func(t *testing.T) {
		src, flows, _ := newLinked(t, "L", "L")

		var throwIDs []string
		for id, n := range src {
			if ln, ok := n.(flow.LinkEventNode); ok && ln.IsLinkSource() {
				throwIDs = append(throwIDs, id)
			}
		}
		require.Len(t, throwIDs, 2)

		clones := wireClones(t, src, flows)

		for _, id := range throwIDs {
			got, err := redirect(t, clones, id)
			require.NoError(t, err)
			require.Len(t, got, 1)
		}
	})

	t.Run("an unpaired throw is left unresolved", func(t *testing.T) {
		thr := lThrow(t, "thr", "orphan") // no catch of this name
		src := map[string]flow.Node{thr.ID(): thr}

		clones := wireClones(t, src, map[string]*flow.SequenceFlow{})

		_, err := redirect(t, clones, thr.ID())
		require.ErrorContains(t, err, "no resolved target")
	})
}
