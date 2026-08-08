package thresher

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/dr-dobermann/gobpm/internal/instance"
	"github.com/dr-dobermann/gobpm/pkg/clock"
	"github.com/dr-dobermann/gobpm/pkg/exec"
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
	// kind says whether this deadline belongs to the node the track is parked
	// on or to a boundary guarding it — the two wake differently (exec.WaitKind).
	kind exec.WaitKind
	// firing marks a wake in progress for this hold, so a later scan does not
	// re-enter one that is slow or already failing (FIX-027 §3.2.1). Cleared by
	// deferHold when the wake fails; the hold is deleted outright when it
	// succeeds.
	firing bool
}

// holdKey identifies a hold by the WAIT it stands for — the definition
// included, not just the track.
//
// A track can hold more than one deadline: an Event-Based Gateway racing two
// timers arms one per arm, and a wait guarded by a timer boundary holds its
// own deadline alongside the boundary's (SRD-071 FR-9a). Keyed by track alone,
// the second hold overwrote the first — and if the lost one was the earlier
// deadline, the wait fired late with nothing to show why.
func holdKey(instanceID, trackID, eDefID string) string {
	return instanceID + "|" + trackID + "|" + eDefID
}

// trackPrefix matches every hold a track owns, for the all-or-nothing
// withdrawal ReleaseWaits performs.
func trackPrefix(instanceID, trackID string) string {
	return instanceID + "|" + trackID + "|"
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
	// arming holds the IN-FLIGHT HoldTimer calls, so a release landing in the
	// middle of an arm can tell that arm it lost (FIX-037 §1.5). Keyed by the
	// arming call's own token, NOT by track: the entry lives only for the
	// duration of one HoldTimer, so the map is bounded by concurrent arms
	// rather than growing with every track the engine has ever seen.
	arming map[uint64]string // token → the track prefix being armed
	// kick re-runs the loop's deadline computation after a hold is added or
	// removed (buffered depth 1 — a coalescing signal, never blocks the caller).
	kick       chan struct{}
	armingNext uint64
	mu         sync.Mutex
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
		arming:  map[uint64]string{},
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
	k := holdKey(h.instanceID, h.trackID, eDefIDOf(h.eDef))
	if cur, ok := ts.holds[k]; ok && cur.firing {
		cur.firing = false
		cur.deadline = next
		ts.holds[k] = cur
	}

	ts.mu.Unlock()

	ts.signal()
}

// beginArm announces an in-flight arm for a track and returns its token. The
// caller MUST pass the token to hold (FIX-037 §1.5).
func (ts *timerService) beginArm(instanceID, trackID string) uint64 {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	ts.armingNext++
	ts.arming[ts.armingNext] = trackPrefix(instanceID, trackID)

	return ts.armingNext
}

// hold registers (or replaces) a deadline and re-arms the loop's nearest-timer.
// It is REFUSED — reporting false — when a release withdrew this track's waits
// while the arm was in flight: release drops the arming token, so a token that
// is gone means the wait this deadline belongs to has been canceled.
//
// Without this, an arm that began before a concurrent ReleaseWaits would
// register its deadline after that release had already scanned and found
// nothing, leaving a zombie deadline that later wakes the instance for a track
// that no longer exists. It is the timer counterpart of HoldSubscription's
// record→arm→confirm (FIX-036 §3.2.4).
func (ts *timerService) hold(h timerHold, token uint64) bool {
	ts.mu.Lock()

	if _, live := ts.arming[token]; !live {
		ts.mu.Unlock()

		return false
	}

	delete(ts.arming, token)
	ts.holds[holdKey(h.instanceID, h.trackID, eDefIDOf(h.eDef))] = h
	ts.mu.Unlock()

	ts.signal()

	return true
}

// releaseOne withdraws the single hold that just fired, leaving a track's other
// deadlines armed — the losing arms of an Event-Based Gateway are withdrawn by
// the instance's own ReleaseWaits when the winner is applied, not here.
// Idempotent.
func (ts *timerService) releaseOne(instanceID, trackID, eDefID string) {
	ts.mu.Lock()
	delete(ts.holds, holdKey(instanceID, trackID, eDefID))
	ts.mu.Unlock()

	ts.signal()
}

// release withdraws EVERY deadline a track holds (the instance terminated, the
// wait was canceled, or a sibling won an EBG race). Idempotent.
func (ts *timerService) release(instanceID, trackID string) {
	prefix := trackPrefix(instanceID, trackID)

	ts.mu.Lock()

	for k := range ts.holds {
		if strings.HasPrefix(k, prefix) {
			delete(ts.holds, k)
		}
	}

	// Cancel any arm still in flight for this track, so a hold that began
	// before this release cannot land after it (FIX-037 §1.5).
	for token, p := range ts.arming {
		if p == prefix {
			delete(ts.arming, token)
		}
	}

	ts.mu.Unlock()

	ts.signal()
}

// eDefIDOf names a hold's definition, tolerating the nil definitions the unit
// traces use — an unnamed hold still keys distinctly per track.
func eDefIDOf(d flow.EventDefinition) string {
	if d == nil {
		return ""
	}

	return d.ID()
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

		// A BOUNDARY deadline wakes the instance TRIGGER-ABSENT: the trigger
		// belongs to the boundary, not to the node the track is parked on, so
		// re-entering that node with it would fire the wrong element. The
		// rebuilt instance re-arms the boundary at its RECORDED deadline
		// (FR-9a), which is already past, and the fire that follows is the
		// ordinary one — a fork at the boundary with the guarded track as its
		// parent, applied by the loop's fireBoundary.
		var pending *instance.PendingTrigger
		if h.kind != exec.WaitBoundary {
			pending = &instance.PendingTrigger{
				TrackID: h.trackID,
				EDef:    h.eDef,
			}
		}

		if ts.wake(h.instanceID, pending) {
			ts.releaseOne(h.instanceID, h.trackID, eDefIDOf(h.eDef))

			continue
		}

		ts.deferHold(h, now.Add(ts.backoff))
	}
}
