package thresher

import (
	"context"
	"sync"
	"time"

	"github.com/dr-dobermann/gobpm/internal/instance"
	"github.com/dr-dobermann/gobpm/pkg/clock"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// timerHold is one dehydratable timer's durable firing plan (SRD-071 FR-6): the
// absolute deadline the engine timer service keeps on behalf of a released
// instance, keyed by (instanceID, trackID), plus the trigger it fires with.
type timerHold struct {
	eDef       flow.EventDefinition
	deadline   time.Time
	instanceID string
	trackID    string
	cycles     int
}

// holdKey identifies a hold by the wait it stands for.
func holdKey(instanceID, trackID string) string {
	return instanceID + "|" + trackID
}

// timerService is the engine-level DURABLE timer holder (SRD-071 FR-6, closes
// #84): a single goroutine over a set of absolute deadlines keyed by
// (instanceID, trackID), replacing the O(waits) per-waiter hub goroutines for
// dehydratable timers. At the nearest deadline it wakes that instance through
// `wake`. It runs on the injected clock, so a test drives it deterministically
// by advancing that clock; production uses the wall clock.
type timerService struct {
	clk   clock.Clock
	wake  func(instanceID string, pending *instance.PendingTrigger)
	holds map[string]timerHold
	// kick re-runs the loop's deadline computation after a hold is added or
	// removed (buffered depth 1 — a coalescing signal, never blocks the caller).
	kick chan struct{}
	mu   sync.Mutex
}

// newTimerService builds the service over clk, waking instances through wake.
func newTimerService(
	clk clock.Clock,
	wake func(string, *instance.PendingTrigger),
) *timerService {
	return &timerService{
		clk:   clk,
		wake:  wake,
		holds: map[string]timerHold{},
		kick:  make(chan struct{}, 1),
	}
}

// hold registers (or replaces) a deadline and re-arms the loop's nearest-timer.
func (ts *timerService) hold(h timerHold) {
	ts.mu.Lock()
	ts.holds[holdKey(h.instanceID, h.trackID)] = h
	ts.mu.Unlock()

	ts.signal()
}

// release withdraws a held timer (the wait completed on wake, the instance
// terminated, or a sibling won an EBG race). Idempotent.
func (ts *timerService) release(instanceID, trackID string) {
	ts.mu.Lock()
	delete(ts.holds, holdKey(instanceID, trackID))
	ts.mu.Unlock()

	ts.signal()
}

// signal coalesces a wake-the-loop notification.
func (ts *timerService) signal() {
	select {
	case ts.kick <- struct{}{}:
	default:
	}
}

// nearest returns the earliest held deadline, or ok=false when none are held.
func (ts *timerService) nearest() (time.Time, bool) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	var (
		best time.Time
		ok   bool
	)

	for _, h := range ts.holds {
		if !ok || h.deadline.Before(best) {
			best, ok = h.deadline, true
		}
	}

	return best, ok
}

// run drives the service until ctx is canceled (engine stop). It waits on the
// nearest deadline (or indefinitely when idle, until a hold kicks it) and fires
// the due timers when the clock reaches them.
func (ts *timerService) run(ctx context.Context) {
	for {
		var fire <-chan time.Time
		if next, ok := ts.nearest(); ok {
			// After(<=0) fires at once — an already-overdue deadline (a hold
			// added past its time) wakes on the next loop turn.
			fire = ts.clk.After(next.Sub(ts.clk.Now()))
		}

		select {
		case <-ctx.Done():
			return
		case <-ts.kick:
			continue // a hold changed — recompute the nearest deadline
		case <-fire:
			ts.fireDue(ctx)
		}
	}
}

// fireDue wakes every instance whose deadline has passed and removes its hold:
// a one-shot firing, so an overdue deadline collapses to a single wake
// (SRD-070 / ADR-033 §2.5). The wake runs inline on the service goroutine — it
// rebuilds and continues the instance; a slow wake serializes the next fire,
// acceptable for the single-engine scope (§4.2).
func (ts *timerService) fireDue(ctx context.Context) {
	now := ts.clk.Now()

	ts.mu.Lock()

	var due []timerHold

	for k, h := range ts.holds {
		if !h.deadline.After(now) {
			due = append(due, h)

			delete(ts.holds, k)
		}
	}

	ts.mu.Unlock()

	for _, h := range due {
		select {
		case <-ctx.Done():
			return
		default:
		}

		ts.wake(h.instanceID, &instance.PendingTrigger{
			TrackID: h.trackID,
			EDef:    h.eDef,
		})
	}
}
