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
	// firing marks a wake in progress for this hold, so a later scan does not
	// re-enter one that is slow or already failing (FIX-027 §3.2.1). Cleared by
	// deferHold when the wake fails; the hold is deleted outright when it
	// succeeds.
	firing bool
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
	clk clock.Clock
	// wake returns whether the instance was actually woken. A hold is the
	// released instance's ONLY way back, so it is surrendered on success and
	// KEPT on failure (FIX-027): the callback has to report which.
	wake  func(instanceID string, pending *instance.PendingTrigger) bool
	holds map[string]timerHold
	// kick re-runs the loop's deadline computation after a hold is added or
	// removed (buffered depth 1 — a coalescing signal, never blocks the caller).
	kick chan struct{}
	mu   sync.Mutex
	// backoff pushes a failed wake's next attempt out. Without it the hold is
	// still due the instant the wake returns, so the loop would re-fire it
	// immediately and spin — retrying as fast as it turns (FIX-027 §3.2.1).
	backoff time.Duration
}

// newTimerService builds the service over clk, waking instances through wake
// and retrying a failed wake after backoff.
func newTimerService(
	clk clock.Clock,
	backoff time.Duration,
	wake func(string, *instance.PendingTrigger) bool,
) *timerService {
	return &timerService{
		clk:     clk,
		wake:    wake,
		backoff: backoff,
		holds:   map[string]timerHold{},
		kick:    make(chan struct{}, 1),
	}
}

// deferHold keeps a hold whose wake FAILED, re-arming it for a later attempt:
// the firing mark is cleared and the deadline moved out, so the service's own
// nearest-deadline loop schedules the retry — no extra machinery, and no spin
// (the hold would otherwise still be due and re-fire at once). The instance
// therefore self-heals as soon as the cause clears, with no scan of the store
// and no engine restart (FIX-027 §3.1.A).
func (ts *timerService) deferHold(h timerHold, next time.Time) {
	ts.mu.Lock()

	// only re-arm if this exact hold is still the registered one: a concurrent
	// re-arm (the instance woke another way and parked again) must win.
	k := holdKey(h.instanceID, h.trackID)
	if cur, ok := ts.holds[k]; ok && cur.firing {
		cur.firing = false
		cur.deadline = next
		ts.holds[k] = cur
	}

	ts.mu.Unlock()

	ts.signal()
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

// fireDue wakes every instance whose deadline has passed, surrendering a hold
// only once its wake has actually succeeded: a fired one-shot timer must not
// fire twice (ADR-033 v.2 §2.5), but a hold is the released instance's ONLY way
// back, so it must outlive an attempt that failed (FIX-027 §2.1). A due hold is
// marked firing for the duration — later scans skip it, so a slow or failing
// wake is never re-entered — then released on success or deferred on failure.
//
// The wake runs inline on the service goroutine: it rebuilds and continues the
// instance, so a slow wake serializes the next fire — acceptable for the
// single-engine scope (SRD-071 §4.2).
func (ts *timerService) fireDue(ctx context.Context) {
	now := ts.clk.Now()

	ts.mu.Lock()

	var due []timerHold

	for k, h := range ts.holds {
		if !h.deadline.After(now) && !h.firing {
			h.firing = true
			ts.holds[k] = h

			due = append(due, h)
		}
	}

	ts.mu.Unlock()

	for _, h := range due {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if ts.wake(h.instanceID, &instance.PendingTrigger{
			TrackID: h.trackID,
			EDef:    h.eDef,
		}) {
			ts.release(h.instanceID, h.trackID)

			continue
		}

		ts.deferHold(h, now.Add(ts.backoff))
	}
}
