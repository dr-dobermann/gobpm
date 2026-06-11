package gateways_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/stretchr/testify/require"
)

func TestNewParallelGateway(t *testing.T) {
	// invalid option (an events option is not a gateway option)
	_, err := gateways.NewParallelGateway(events.WithParallel())
	require.Error(t, err)

	// valid options
	_, err = gateways.NewParallelGateway(
		foundation.WithID("parallel gateway #1"),
		foundation.WithDoc("forks all outgoing flows", foundation.PlainText),
		options.WithName("my first parallel gateway"),
		gateways.WithDirection(gateways.Diverging))
	require.NoError(t, err)
}

func TestParallelGatewayExec(t *testing.T) {
	pg, err := gateways.NewParallelGateway(
		gateways.WithDirection(gateways.Diverging))
	require.NoError(t, err)

	// Node() returns the concrete gateway (so flow-dispatch finds the executor).
	concrete, ok := pg.Node().(*gateways.ParallelGateway)
	require.True(t, ok)
	require.Same(t, pg, concrete)

	// one incoming, two outgoing flows.
	nodes := getDummyNodes(3)
	_, err = flow.Link(nodes[0], pg)
	require.NoError(t, err)

	f1, err := flow.Link(pg, nodes[1])
	require.NoError(t, err)

	f2, err := flow.Link(pg, nodes[2])
	require.NoError(t, err)

	// Exec activates every outgoing flow, unconditionally (re is unused).
	flows, err := pg.Exec(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, flows, 2)

	got := map[string]bool{}
	for _, f := range flows {
		got[f.ID()] = true
	}

	require.True(t, got[f1.ID()])
	require.True(t, got[f2.ID()])
}

func TestParallelGatewayArrive(t *testing.T) {
	// converging gateway: two incoming, one outgoing.
	pg, err := gateways.NewParallelGateway(
		gateways.WithDirection(gateways.Converging))
	require.NoError(t, err)

	nodes := getDummyNodes(3)
	f1, err := flow.Link(nodes[0], pg)
	require.NoError(t, err)
	f2, err := flow.Link(nodes[1], pg)
	require.NoError(t, err)
	_, err = flow.Link(pg, nodes[2])
	require.NoError(t, err)

	require.Len(t, pg.Incoming(), 2)

	// first arrival: not complete, the arriving track id is recorded.
	complete, merged := pg.Arrive(f1.ID(), "track-A")
	require.False(t, complete)
	require.Nil(t, merged)

	// second (completing) arrival: complete; returns the absorbed track id, not
	// the survivor's (track-B is the completing arrival).
	complete, merged = pg.Arrive(f2.ID(), "track-B")
	require.True(t, complete)
	require.Equal(t, []string{"track-A"}, merged)

	// the arrival state reset on fire: a fresh round starts incomplete again.
	complete, merged = pg.Arrive(f1.ID(), "track-C")
	require.False(t, complete)
	require.Nil(t, merged)
}

func TestParallelGatewayArriveConcurrent(t *testing.T) {
	pg, err := gateways.NewParallelGateway(
		gateways.WithDirection(gateways.Converging))
	require.NoError(t, err)

	nodes := getDummyNodes(3)
	f1, err := flow.Link(nodes[0], pg)
	require.NoError(t, err)
	f2, err := flow.Link(nodes[1], pg)
	require.NoError(t, err)
	_, err = flow.Link(pg, nodes[2])
	require.NoError(t, err)

	// two tracks arrive concurrently; the mutex serializes them so exactly one
	// gets complete=true (the run -race detector backs the atomicity).
	type res struct {
		merged   []string
		complete bool
	}

	results := make(chan res, 2)
	flows := []string{f1.ID(), f2.ID()}
	trackIDs := []string{"track-0", "track-1"}

	for i := range 2 {
		go func(flowID, trackID string) {
			c, m := pg.Arrive(flowID, trackID)
			results <- res{complete: c, merged: m}
		}(flows[i], trackIDs[i])
	}

	completes := 0
	for range 2 {
		if (<-results).complete {
			completes++
		}
	}

	require.Equal(t, 1, completes, "exactly one arrival completes the join")
}

func TestParallelGatewayClone(t *testing.T) {
	pg, err := gateways.NewParallelGateway(
		gateways.WithDirection(gateways.Diverging))
	require.NoError(t, err)

	nodes := getDummyNodes(3)
	_, err = flow.Link(nodes[0], pg)
	require.NoError(t, err)
	_, err = flow.Link(pg, nodes[1])
	require.NoError(t, err)
	_, err = flow.Link(pg, nodes[2])
	require.NoError(t, err)

	require.NotEmpty(t, pg.Outgoing())
	require.NotEmpty(t, pg.Incoming())

	clone, ok := pg.Clone().(*gateways.ParallelGateway)
	require.True(t, ok)

	// independent object, same id, shared configuration.
	require.NotSame(t, pg, clone)
	require.Equal(t, pg.ID(), clone.ID())
	require.Equal(t, pg.Direction(), clone.Direction())

	// flows empty, no container.
	require.Empty(t, clone.Outgoing())
	require.Empty(t, clone.Incoming())
	require.Nil(t, clone.Container())
}
