package membroker

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// recLogger records emitted Debug messages for flow-logging assertions.
type recLogger struct{ msgs []string }

func (l *recLogger) Debug(m string, _ ...any) { l.msgs = append(l.msgs, m) }
func (l *recLogger) Info(string, ...any)      {}
func (l *recLogger) Warn(string, ...any)      {}
func (l *recLogger) Error(string, ...any)     {}

// TestPublishRouteLogging verifies the broker emits a Debug line for each route
// decision — subscribe, keyed delivery, wildcard delivery, buffered miss — at
// Debug level (off by default, so normal runs stay quiet). This is the
// visibility a developer turns on to watch message routing.
func TestPublishRouteLogging(t *testing.T) {
	rec := &recLogger{}
	b := New(WithLogger(rec))
	ctx := context.Background()

	_, err := b.Subscribe(ctx, "pay", "k1") // keyed
	require.NoError(t, err)
	_, err = b.Subscribe(ctx, "note") // wildcard
	require.NoError(t, err)

	require.NoError(t, b.Publish(ctx, env("pay", "k1")))  // routed (keyed)
	require.NoError(t, b.Publish(ctx, env("note", "")))   // routed (wildcard)
	require.NoError(t, b.Publish(ctx, env("ghost", "z"))) // buffered (no sub)

	for _, want := range []string{
		"membroker: subscribed",
		"membroker: routed (keyed)",
		"membroker: routed (wildcard)",
		"membroker: buffered (no subscriber)",
	} {
		require.Contains(t, rec.msgs, want)
	}
}
