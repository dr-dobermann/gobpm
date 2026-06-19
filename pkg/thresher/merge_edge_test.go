package thresher_test

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

// TestMergedIntoRecorded checks the FR-8 merge edge on a Parallel synchronizing
// join: the absorbed track records MergedInto = the survivor, and the survivor's
// lineage (ParentID) is untouched — a forward, acyclic edge (FR-5b).
//
//	start → split ─┬→ join → end
//	               └→ ┘
func TestMergedIntoRecorded(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	proc, err := process.New("merge-edge")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	split, err := gateways.NewParallelGateway(gateways.WithDirection(gateways.Diverging))
	require.NoError(t, err)
	join, err := gateways.NewParallelGateway(gateways.WithDirection(gateways.Converging))
	require.NoError(t, err)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, split, join, end} {
		require.NoError(t, proc.Add(e))
	}

	link(t, start, split)
	link(t, split, join) // branch 1
	link(t, split, join) // branch 2
	link(t, join, end)

	th, cancel := runEngine(t, proc)
	defer cancel()

	h, err := th.StartProcess(proc.ID())
	require.NoError(t, err)

	ctx, cc := context.WithTimeout(context.Background(), 3*time.Second)
	defer cc()
	st, err := h.WaitCompletion(ctx)
	require.NoError(t, err)
	require.Equal(t, thresher.StateCompleted, st)

	hist := h.History()

	var merged, survivor *thresher.TokenPath

	for i := range hist {
		if hist[i].MergedInto != "" {
			merged = &hist[i]
		}
	}

	require.NotNil(t, merged, "one branch must record a MergedInto edge")

	for i := range hist {
		if hist[i].TrackID == merged.MergedInto {
			survivor = &hist[i]
		}
	}

	require.NotNil(t, survivor, "MergedInto must point at a real track")
	require.Empty(t, survivor.MergedInto, "the survivor is not itself merged")

	// FR-5b: the merge edge (MergedInto) is separate from lineage; every
	// ParentID chain stays acyclic (the merge is not folded into a parent).
	byID := make(map[string]thresher.TokenPath, len(hist))
	for _, p := range hist {
		byID[p.TrackID] = p
	}

	for _, p := range hist {
		seen := map[string]bool{p.TrackID: true}
		for cur := p.ParentID; cur != ""; cur = byID[cur].ParentID {
			require.False(t, seen[cur], "ParentID chain must be acyclic (FR-5b)")
			seen[cur] = true
		}
	}
}
