package thresher_test

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/auth"
	"github.com/dr-dobermann/gobpm/pkg/auth/allowall"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

// filterAuthz is an allow-all authorizer that also implements the ObservationFilter
// capability, denying delivery when deny is set (else pass-through).
type filterAuthz struct {
	auth.AuthorizationProvider
	deny bool
}

func (a filterAuthz) FilterObservation(
	_ any, ev observability.Fact,
) (observability.Fact, bool) {
	return ev, !a.deny
}

// runEngineWithAuthz builds and runs an engine with a specific authorizer.
func runEngineWithAuthz(
	t *testing.T, proc *process.Process, authz auth.AuthorizationProvider,
) (*thresher.Thresher, context.CancelFunc) {
	t.Helper()

	th, err := thresher.New("test-authz-"+proc.ID(),
		thresher.WithAuthorizationProvider(authz))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, th.Run(ctx))
	_, err = th.RegisterProcess(proc)
	require.NoError(t, err)

	return th, cancel
}

// TestEngineObserveReceivesInstanceEvents (SRD-041 T-2): an engine-scope observer
// registered via Thresher.Observe receives a launched instance's lifecycle and
// node-progress events, each carrying instance_id — the same buffered/lossy
// contract as the instance handle.
func TestEngineObserveReceivesInstanceEvents(t *testing.T) {
	proc := linearProcess(t, "eng-obs", 200*time.Millisecond)
	th, cancel := runEngine(t, proc)
	defer cancel()

	c := &collector{}
	sub := th.Observe(c) // registered before any instance exists

	h, err := th.StartLatest(proc.ID())
	require.NoError(t, err)

	ctx, cc := context.WithTimeout(context.Background(), 3*time.Second)
	defer cc()

	state, err := h.WaitCompletion(ctx)
	require.NoError(t, err)
	require.Equal(t, thresher.StateCompleted, state)

	sub.Cancel() // drains buffered events

	require.True(t, c.sawCompleted(), "engine observer should see Completed")
	require.True(t, c.sawNodeProgress(), "engine observer should see node progress")

	// Every delivered event carries the originating instance id, on both the
	// promoted field and the canonical details key.
	c.mu.Lock()
	defer c.mu.Unlock()
	require.NotEmpty(t, c.events)

	for _, e := range c.events {
		require.Equal(t, h.ID(), e.Details[observability.AttrInstanceID],
			"every delivered Fact carries its instance_id in Details")
	}
}

// TestHandleObserveFilterGovernsVisibility (SRD-041 T-8): the ObservationFilter
// applies to instance-scope handle observers too — a denying filter blocks
// delivery (denied ≠ dropped), a pass-through one leaves it intact.
func TestHandleObserveFilterGovernsVisibility(t *testing.T) {
	t.Run("deny blocks delivery", func(t *testing.T) {
		proc := linearProcess(t, "flt-deny", 150*time.Millisecond)
		th, cancel := runEngineWithAuthz(t, proc, filterAuthz{
			AuthorizationProvider: allowall.New(),
			deny:                  true,
		})
		defer cancel()

		h, err := th.StartLatest(proc.ID())
		require.NoError(t, err)

		c := &collector{}
		sub := h.Observe(c)

		ctx, cc := context.WithTimeout(context.Background(), 3*time.Second)
		defer cc()

		_, err = h.WaitCompletion(ctx)
		require.NoError(t, err)

		sub.Cancel()

		c.mu.Lock()
		defer c.mu.Unlock()
		require.Empty(t, c.events, "a denied observer receives nothing")
		require.Zero(t, sub.Dropped(), "a policy denial is not a counted drop")
	})

	t.Run("pass-through delivers", func(t *testing.T) {
		proc := linearProcess(t, "flt-pass", 150*time.Millisecond)
		th, cancel := runEngineWithAuthz(t, proc, filterAuthz{
			AuthorizationProvider: allowall.New(),
			deny:                  false,
		})
		defer cancel()

		h, err := th.StartLatest(proc.ID())
		require.NoError(t, err)

		c := &collector{}
		sub := h.Observe(c)

		ctx, cc := context.WithTimeout(context.Background(), 3*time.Second)
		defer cc()

		_, err = h.WaitCompletion(ctx)
		require.NoError(t, err)

		sub.Cancel()

		require.True(t, c.sawCompleted(), "a pass-through filter delivers normally")
	})
}

// TestEngineObserveCancelIsIdempotent (SRD-041 T-2): a second Cancel on an
// engine subscription is a no-op, matching the handle subscription's contract.
func TestEngineObserveCancelIsIdempotent(t *testing.T) {
	proc := linearProcess(t, "eng-obs-cancel", 0)
	th, cancel := runEngine(t, proc)
	defer cancel()

	sub := th.Observe(&collector{})
	sub.Cancel()
	sub.Cancel() // idempotent
}
