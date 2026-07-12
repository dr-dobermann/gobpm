package thresher

import (
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
	cancel  func()
	dropped *atomic.Uint64
}

// Dropped reports how many Facts were dropped because the observer fell behind
// the buffer (SRD-018 FR-9). Best-effort, monotonic.
func (s *Subscription) Dropped() uint64 {
	return s.dropped.Load()
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
// is recovered. Cancel the returned Subscription to stop observing. The delivered
// Fact already carries instance_id in its Details (stamped by the instance).
func (h *InstanceHandle) Observe(o Observer) *Subscription {
	ch := make(chan observability.Fact, observerBuffer)
	done := make(chan struct{})

	var dropped atomic.Uint64

	go func() {
		defer close(done)

		for f := range ch {
			deliver(o, f)
		}
	}()

	// The instance-scope visibility filter (ADR-013 §2.11): the policy is
	// per-recipient with no scope carve-out, so it gates handle observers too.
	// Asserted once here at registration; absent ⇒ pass-through.
	filter, _ := h.inst.AuthorizationProvider().(observability.ObservationFilter)

	cancelReg := h.inst.AddObserver(func(f observability.Fact) {
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
	})

	var once sync.Once

	return &Subscription{
		dropped: &dropped,
		cancel: func() {
			once.Do(func() {
				// Deregister first: AddObserver's cancel takes the instance's
				// observer write-lock, which fences any in-flight fan-out, so no
				// send is in progress once it returns — making close(ch) safe (no
				// send-on-closed-channel). Then drain to completion.
				cancelReg()
				close(ch)
				<-done
			})
		},
	}
}

// deliver calls the observer, containing any panic so one bad observer cannot
// crash the drain goroutine or affect others (ADR-013 §5).
func deliver(o Observer, f observability.Fact) {
	defer func() { _ = recover() }()

	o.OnFact(f)
}
