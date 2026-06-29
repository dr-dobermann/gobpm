package thresher

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/messaging"
	"github.com/dr-dobermann/gobpm/pkg/messaging/membroker"
	"github.com/stretchr/testify/require"
)

// TestLatestSupersedesAutoStart verifies that registering a newer version of a
// key transfers auto-start to it: only the latest version owns the trigger, so
// a single fired event spawns exactly one instance, not one per registered
// version (SRD-031.A FR-7, T-9).
func TestLatestSupersedesAutoStart(t *testing.T) {
	broker := membroker.New()

	th, err := New("supersede", WithMessageBroker(broker))
	require.NoError(t, err)

	proc := msgStartProcess(t, "p-sup", "order placed")
	reg1, err := th.RegisterProcess(proc)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, th.Run(ctx))

	// re-registering the same key after Run supersedes v1's live starter with v2's.
	reg2, err := th.RegisterProcess(proc)
	require.NoError(t, err)
	require.Equal(t, reg1.Key(), reg2.Key())
	require.Equal(t, 2, reg2.Version())

	// one trigger spawns exactly one instance — the superseded v1 starter is no
	// longer live, so it does not also fire.
	require.NoError(t, broker.Publish(ctx,
		messaging.Envelope{Name: "order placed", Payload: "x"}))
	require.Eventually(t, func() bool { return instanceCount(th) == 1 },
		3*time.Second, 10*time.Millisecond,
		"the superseding version owns the trigger")
	require.Never(t, func() bool { return instanceCount(th) > 1 },
		300*time.Millisecond, 50*time.Millisecond,
		"the superseded version must not also spawn")

	// the live starter set is the latest version's only.
	require.Len(t, th.Starters(), 1)
}

// TestUnregisterLatestPromotesPrevious verifies promote-on-removal: removing the
// latest version makes the now-newest remaining version the live auto-start
// version, so it owns the trigger again. This is how a user re-activates a
// previous version's auto-start (SRD-031.A FR-8, T-11).
func TestUnregisterLatestPromotesPrevious(t *testing.T) {
	broker := membroker.New()

	th, err := New("promote", WithMessageBroker(broker))
	require.NoError(t, err)

	proc := msgStartProcess(t, "p-prom", "order placed")
	reg1, err := th.RegisterProcess(proc)
	require.NoError(t, err)
	reg2, err := th.RegisterProcess(proc)
	require.NoError(t, err)
	require.Equal(t, 1, reg1.Version())
	require.Equal(t, 2, reg2.Version())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, th.Run(ctx))

	// removing the latest (v2) promotes v1 to the live auto-start version.
	require.NoError(t, th.UnregisterVersion(reg2))

	th.m.Lock()
	regs := th.registrations[reg1.Key()]
	th.m.Unlock()
	require.Len(t, regs, 1)
	require.Equal(t, 1, regs[0].version)

	// the promoted v1 owns the trigger again: a message spawns an instance.
	require.NoError(t, broker.Publish(ctx,
		messaging.Envelope{Name: "order placed", Payload: "x"}))
	require.Eventually(t, func() bool { return instanceCount(th) == 1 },
		3*time.Second, 10*time.Millisecond,
		"removing the latest promotes the previous version to live auto-start")

	// Starters now lists the promoted previous version.
	require.Len(t, th.Starters(), 1)
}
