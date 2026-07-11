package thresher

import (
	"sync"
	"sync/atomic"

	"github.com/dr-dobermann/gobpm/pkg/auth"
	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// producer is the engine's single observable-event sink (SRD-041 FR-4): one
// Emit call feeds two channels — the operator-log echo and the engine-scope
// observer stream. It is the implementation behind EngineRuntime.ObservationSink,
// shared by the instance loop, the hub, and the dispatcher, so every emitter
// reaches one producer.
type producer struct {
	log      observability.Logger
	redactor observability.LogRedactor
	filter   observability.ObservationFilter
	subs     map[uint64]*engineSub
	mu       sync.Mutex
	nextID   uint64
}

// engineSub is one engine-scope observer registration: its buffered channel, the
// drain goroutine's done signal, its drop counter, and the Observer itself (the
// per-recipient key handed to the visibility filter).
type engineSub struct {
	ch      chan Event
	done    chan struct{}
	dropped *atomic.Uint64
	obs     Observer
	id      uint64
}

// newProducer builds the engine sink bound to the configured logger, asserting
// the optional visibility capabilities against the authorization provider once
// at wiring (SRD-041 FR-8): an authorizer implementing neither leaves both
// channels pass-through, and no per-event assertion is paid.
func newProducer(
	log observability.Logger,
	authz auth.AuthorizationProvider,
) *producer {
	redactor, _ := authz.(observability.LogRedactor)
	filter, _ := authz.(observability.ObservationFilter)

	return &producer{
		log:      log,
		redactor: redactor,
		filter:   filter,
		subs:     map[uint64]*engineSub{},
	}
}

// Emit writes the event's operator-log echo and fans it out to the engine-scope
// observers (the ObsSink contract). It is safe to call from any goroutine: the
// echo is a logger call and the fan-out is lock-guarded with non-blocking sends,
// so a slow observer drops events rather than stalling the caller (NFR-2).
func (p *producer) Emit(ev observability.ObsEvent) {
	p.echo(ev)
	p.fanout(ev)
}

// echo writes the operator-log record, applying the LogRedactor first: a
// redactor may transform the event or suppress the record entirely (ok=false).
// Echo itself skips the stream-only kinds and picks the level.
func (p *producer) echo(ev observability.ObsEvent) {
	if p.redactor != nil {
		redacted, ok := p.redactor.RedactLog(ev)
		if !ok {
			return
		}

		ev = redacted
	}

	observability.Echo(p.log, ev)
}

// fanout delivers ev to every engine-scope observer, applying the
// ObservationFilter per recipient (a denial is not a counted drop). The buffered
// send is non-blocking; a full buffer counts a drop. The lock spans the dispatch
// so unsubscribe can fence any in-flight send before closing a channel.
func (p *producer) fanout(ev observability.ObsEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, s := range p.subs {
		out := ev
		if p.filter != nil {
			filtered, ok := p.filter.FilterObservation(s.obs, ev)
			if !ok {
				continue // policy-denied — not a counted drop
			}

			out = filtered
		}

		select {
		case s.ch <- toPublicEvent(out):
		default:
			s.dropped.Add(1)
		}
	}
}

// subscribe registers o on the engine-scope stream and returns its Subscription,
// mirroring the instance handle's Observe: a buffered, lossy, drop-counted,
// panic-contained delivery drained by one goroutine.
func (p *producer) subscribe(o Observer) *Subscription {
	ch := make(chan Event, observerBuffer)
	done := make(chan struct{})

	var dropped atomic.Uint64

	go func() {
		defer close(done)

		for ev := range ch {
			deliver(o, ev)
		}
	}()

	p.mu.Lock()
	p.nextID++
	id := p.nextID
	p.subs[id] = &engineSub{ch: ch, done: done, dropped: &dropped, obs: o, id: id}
	p.mu.Unlock()

	var once sync.Once

	return &Subscription{
		dropped: &dropped,
		cancel: func() {
			once.Do(func() {
				// Delete under the lock first: fanout holds the same lock across
				// its dispatch, so once delete returns no send to ch is in
				// flight, making close(ch) safe (no send-on-closed). Then drain.
				p.mu.Lock()
				delete(p.subs, id)
				p.mu.Unlock()
				close(ch)
				<-done
			})
		},
	}
}

// Observe registers o on the engine-scope observation stream (SRD-041 FR-5): it
// receives every engine-kind event AND every running instance's events (each
// carrying instance_id in Details). Same buffered/lossy/drop-counted contract as
// the instance handle's Observe; cancel the returned Subscription to stop.
func (t *Thresher) Observe(o Observer) *Subscription {
	return t.producer.subscribe(o)
}
