package membroker_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/messaging"
	"github.com/dr-dobermann/gobpm/pkg/messaging/membroker"
	"github.com/stretchr/testify/require"
)

// TestMembrokerHonorsCancelledContext pins FIX-010 §3.2.6: Publish and Subscribe
// must return ctx.Err() on an already-cancelled context and not mutate broker
// state (no buffered message, no dangling subscription).
func TestMembrokerHonorsCancelledContext(t *testing.T) {
	b := membroker.New()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	require.ErrorIs(t, b.Publish(ctx, messaging.Envelope{Name: "n"}),
		context.Canceled)

	sub, err := b.Subscribe(ctx, "n")
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, sub)

	// the broker is not corrupted: a live subscribe + publish still round-trips,
	// and the cancelled Publish above was not buffered into this subscription.
	live, err := b.Subscribe(context.Background(), "n")
	require.NoError(t, err)

	require.NoError(t, b.Publish(context.Background(),
		messaging.Envelope{Name: "n", Payload: "ok"}))

	select {
	case env := <-live.C():
		require.Equal(t, "ok", env.Payload)
	default:
		t.Fatal("live subscriber did not receive the post-cancel message")
	}
}
