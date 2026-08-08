package thresher

import (
	"fmt"
	"runtime/debug"
	"sync"
	"sync/atomic"

	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// observerBuffer is the per-observer Fact-channel depth. A slower observer than
// this many buffered Facts drops the excess (Subscription.Dropped) rather than
// blocking the engine (ADR-013 §2.2; the buffer size is an SRD-018 choice).
const observerBuffer = 64

// Observer is the canonical observation receiver — a type alias of
// observability.Observer (ADR-013 v.2 §2.8). A host implements it once (OnFact)
// and registers it on an instance handle (InstanceHandle.Observe) or the engine
// (Thresher.Observe); the alias keeps the historical thresher.Observer spelling.
// The event vocabulary is observability.Kind / KindXxx — there is one canonical
// Fact type from emitter to delivery (no thresher-specific projection).
type Observer = observability.Observer

// Subscription is a live observer registration on a Fact stream.
type Subscription struct {
	cancel   func()
	dropped  *atomic.Uint64
	panicked *atomic.Uint64
}

// Dropped reports how many Facts were dropped because the observer fell behind
// the buffer (SRD-018 FR-9). Best-effort, monotonic.
func (s *Subscription) Dropped() uint64 {
	return s.dropped.Load()
}

// Panicked reports how many times this observer's OnFact panicked and was
// contained (ADR-013 v.2 §5). Best-effort, monotonic — the companion to
// Dropped(), and the two mean different things: a non-zero Dropped means the
// engine outran the observer, a non-zero Panicked means the observer itself is
// broken and its Facts were never processed.
func (s *Subscription) Panicked() uint64 {
	return s.panicked.Load()
}

// Cancel deregisters the observer and drains any buffered Facts, then stops the
// drain goroutine. Idempotent.
func (s *Subscription) Cancel() {
	s.cancel()
}

// Observe registers o on the instance's Fact stream (SRD-018, ADR-013 §2.8).
// Delivery is best-effort and lossy: Facts are buffered per observer and drained
// by one goroutine; if the observer is slower than the buffer, the excess is
// dropped (Subscription.Dropped) and the engine never blocks. A panicking OnFact
// is recovered, logged and counted (Subscription.Panicked). Cancel the returned
// Subscription to stop observing. The delivered Fact already carries instance_id
// in its Details (stamped by the instance).
func (h *InstanceHandle) Observe(o Observer) *Subscription {
	ch := make(chan observability.Fact, observerBuffer)
	done := make(chan struct{})

	var dropped, panicked atomic.Uint64

	// The logger is engine-level and stable across an instance rebuild, so it is
	// captured once here rather than re-read from the (swappable) instance on
	// every delivery. It is never nil: WithLogger rejects nil and the default is
	// slog.Default() (ADR-002 v.2's visible-by-default posture).
	log := h.current().Logger()

	go func() {
		defer close(done)

		for f := range ch {
			deliverObserved(log, o, f, &panicked)
		}
	}()

	// The instance-scope visibility filter (ADR-013 §2.11): the policy is
	// per-recipient with no scope carve-out, so it gates handle observers too.
	// Asserted once here at registration; absent ⇒ pass-through.
	filter, _ := h.current().AuthorizationProvider().(observability.ObservationFilter)

	fanout := func(f observability.Fact) {
		if filter != nil {
			filtered, ok := filter.FilterObservation(o, f)
			if !ok {
				return // policy-denied — not a counted drop
			}

			f = filtered
		}

		select {
		case ch <- f:
		default:
			dropped.Add(1)
		}
	}

	// Record on the HANDLE, then attach to the instance it currently speaks
	// for. Registering only on the instance object meant the subscription died
	// silently at the first dehydration, because a rebuild replaces that object
	// (FIX-038 §1.8); the handle re-attaches this entry on every adopt.
	ho := &handleObserver{fanout: fanout}

	h.obsMu.Lock()

	if h.observers == nil {
		h.observers = map[uint64]*handleObserver{}
	}

	h.nextObs++
	obsID := h.nextObs
	h.observers[obsID] = ho
	ho.cancel = h.current().AddObserver(fanout)

	h.obsMu.Unlock()

	var once sync.Once

	return &Subscription{
		dropped:  &dropped,
		panicked: &panicked,
		cancel: func() {
			once.Do(func() {
				// Deregister first: AddObserver's cancel takes the instance's
				// observer write-lock, which fences any in-flight fan-out, so no
				// send is in progress once it returns — making close(ch) safe (no
				// send-on-closed-channel). Then drain to completion.
				h.obsMu.Lock()
				delete(h.observers, obsID)
				cancelReg := ho.cancel
				h.obsMu.Unlock()

				if cancelReg != nil {
					cancelReg()
				}

				close(ch)
				<-done
			})
		},
	}
}

// deliver calls the observer, containing any panic so one bad observer cannot
// crash the drain goroutine or affect others (ADR-013 v.2 §5), and RETURNING
// what it recovered so the caller can report it. A nil recovered value means
// OnFact returned normally: since Go 1.21 a panic(nil) is recovered as a
// non-nil *runtime.PanicNilError, and every module pins toolchain go1.25.12, so
// nil is an unambiguous "no panic" rather than a lost one.
//
// The stack is captured only when wantStack is set. A broken observer panics on
// every Fact, and debug.Stack formats the whole goroutine stack, so paying for
// it per delivery would be a hot-path cost for information the first record
// already carries (FIX-035 §3.1.D).
func deliver(
	o Observer, f observability.Fact, wantStack bool,
) (recovered any, stack []byte) {
	defer func() {
		if r := recover(); r != nil {
			recovered = r

			if wantStack {
				stack = debug.Stack()
			}
		}
	}()

	o.OnFact(f)

	return nil, nil
}

// deliverObserved calls o.OnFact under panic containment and records any panic,
// completing ADR-013 v.2 §5's drop-with-WARNING (the recover and the drop landed
// with SRD-018; the warning did not, so a broken observer was indistinguishable
// from a working one).
//
// The FIRST panic per subscription logs at Warn with a stack; later ones log at
// Debug; every one increments panicked, which Subscription.Panicked() exposes.
// Two ADR-022 v.1 §2.4 rules fix that shape: Error is reserved for failures that
// affected ENGINE state, and a contained observer panic affects none — Warn is
// the "degraded but continuing" level. And its hot-path corollary forbids any
// per-event record above Debug, so bounding the Warn to one per subscription is
// what makes the loud record legitimate at all. The counter, not the log, is the
// authority on how often it happened.
func deliverObserved(
	log observability.Logger,
	o Observer,
	f observability.Fact,
	panicked *atomic.Uint64,
) {
	first := panicked.Load() == 0

	r, stack := deliver(o, f, first)
	if r == nil {
		return
	}

	panicked.Add(1)

	// two identity pairs, plus the stack pair the Warn branch appends.
	args := make([]any, 0, 6)
	args = append(args,
		observability.AttrObserverType, fmt.Sprintf("%T", o),
		observability.AttrError, fmt.Sprint(r),
	)

	if !first {
		log.Debug("observer panicked", args...)

		return
	}

	log.Warn("observer panicked; its Facts are lost until it is fixed",
		append(args, "stack", string(stack))...)
}
