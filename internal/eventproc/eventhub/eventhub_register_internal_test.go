package eventhub

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/stretchr/testify/require"
)

// fixedClock reports a constant time — used to make a timer waiter's
// construction-time "not in the past" check pass while its real-time Service
// duration is non-positive (so Service fails deterministically).
type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

func (c fixedClock) After(d time.Duration) <-chan time.Time {
	return time.After(d)
}

// timerAt builds a Time-triggered timer definition firing at fireAt.
func timerAt(fireAt time.Time) flow.EventDefinition {
	return events.MustTimerEventDefinition(
		goexpr.Must(
			nil,
			data.MustItemDefinition(values.NewVariable(time.Now())),
			func(_ context.Context, _ data.Source) (data.Value, error) {
				return values.NewVariable(fireAt), nil
			}),
		nil, nil)
}

// TestRegisterEventConcurrent is the regression for the RegisterEvent TOCTOU
// (audit 1.5 / FIX-003 C): N goroutines register distinct processors for the
// SAME event definition. Under the old check-then-insert split they each
// created a waiter and the inserts clobbered each other (one surviving waiter
// holding a single processor, N-1 orphaned serving goroutines). Under the
// single critical section there is exactly ONE waiter holding ALL processors.
// Run under -race.
func TestRegisterEventConcurrent(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	hub, err := New(enginert.Default())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, hub.Start(ctx))

	// a far-future timer shared by all registrations.
	eDef := timerAt(time.Now().Add(time.Hour))

	const n = 12

	procs := make([]eventproc.EventProcessor, n)
	for i := range procs {
		p := mockeventproc.NewMockEventProcessor(t)
		p.EXPECT().ID().Return(fmt.Sprintf("proc-%d", i)).Maybe()
		procs[i] = p
	}

	errCh := make(chan error, n)

	var wg sync.WaitGroup

	for i := range procs {
		wg.Go(func() {
			errCh <- hub.RegisterEvent(procs[i], eDef)
		})
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		require.NoError(t, err)
	}

	// exactly one waiter for the shared event definition...
	hub.m.RLock()
	defer hub.m.RUnlock()

	require.Len(t, hub.waiters, 1, "concurrent same-id registration -> 1 waiter")

	for _, w := range hub.waiters {
		require.Len(t, w.EventProcessors(), n,
			"the single waiter must hold every processor")
	}
}

// TestRegisterEventServiceFailure proves a waiter whose Service() fails is
// never left in the map (FIX-003 C): Service runs before the insert. The
// fixed clock makes construction accept a timer firing "now" while Service
// computes a non-positive real duration and fails.
func TestRegisterEventServiceFailure(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	// fixed clock in the real past: construction's "fireAt not before
	// clock.Now()" passes (fireAt == clock.Now()), but Service's real
	// time.Until(fireAt) is negative -> "duration is not positive".
	past := time.Now().Add(-time.Hour)
	rt := enginert.Default().WithClock(fixedClock{t: past})

	hub, err := New(rt)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, hub.Start(ctx))

	p := mockeventproc.NewMockEventProcessor(t)
	p.EXPECT().ID().Return("p").Maybe()

	err = hub.RegisterEvent(p, timerAt(past))
	require.Error(t, err, "Service must fail on a non-positive duration")

	hub.m.RLock()
	defer hub.m.RUnlock()

	require.Empty(t, hub.waiters,
		"a waiter that failed to start must not be left in the map")
}

// TestUnregisterEventFullChain proves the now-real unregistration chain
// (FIX-003 B): unregistering the only processor of a running waiter removes
// the now-empty waiter from the hub (the empty->Stop->RemoveWaiter path that
// was dead while RemoveEventProcessor was a no-op).
func TestUnregisterEventFullChain(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	hub, err := New(enginert.Default())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, hub.Start(ctx))

	p := mockeventproc.NewMockEventProcessor(t)
	p.EXPECT().ID().Return("p").Maybe()

	eDef := timerAt(time.Now().Add(time.Hour))
	require.NoError(t, hub.RegisterEvent(p, eDef))

	hub.m.RLock()
	require.Len(t, hub.waiters, 1)
	hub.m.RUnlock()

	require.NoError(t, hub.UnregisterEvent(p, eDef.ID()))

	hub.m.RLock()
	require.Empty(t, hub.waiters,
		"the last processor leaving stops and removes the waiter")
	hub.m.RUnlock()
}
