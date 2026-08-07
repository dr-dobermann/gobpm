package thresher

import (
	"fmt"
	"runtime/debug"
	"sync"
	"sync/atomic"

	"github.com/dr-dobermann/gobpm/pkg/auth"
	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// producer is the engine's single observable-event sink (SRD-041 FR-4): one
// Report call feeds two channels — the operator-log echo and the engine-scope
// observer stream. It is the implementation behind EngineRuntime.Reporter,
// shared by the instance loop, the hub, and the dispatcher, so every emitter
// reaches one producer.
type producer struct {
	log      observability.Logger
	redactor observability.LogRedactor
	filter   observability.ObservationFilter
	subs     map[uint64]*engineSub
	mu       sync.Mutex
	nextID   uint64
	// panics contained in the two host-supplied hooks, counted separately so
	// each gets its own one-shot Warn (FIX-036 §1.5).
	redactorPanicked atomic.Uint64
	filterPanicked   atomic.Uint64
}

// engineSub is one engine-scope observer registration: its buffered channel, the
// drain goroutine's done signal, its drop counter, and the Observer itself (the
// per-recipient key handed to the visibility filter).
type engineSub struct {
	ch      chan observability.Fact
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

// Report writes the event's operator-log echo and fans it out to the engine-scope
// observers (the Reporter contract). It is safe to call from any goroutine: the
// echo is a logger call and the fan-out is lock-guarded with non-blocking sends,
// so a slow observer drops events rather than stalling the caller (NFR-2).
func (p *producer) Report(ev observability.Fact) {
	p.echo(ev)
	p.fanout(ev)
}

// echo writes the operator-log record, applying the LogRedactor first: a
// redactor may transform the event or suppress the record entirely (ok=false).
// Echo itself skips the stream-only kinds and picks the level.
func (p *producer) echo(ev observability.Fact) {
	if p.redactor != nil {
		redacted, ok := p.redact(ev)
		if !ok {
			return
		}

		ev = redacted
	}

	observability.Echo(p.log, ev)
}

// redact applies the host's LogRedactor under containment. A panicking redactor
// SUPPRESSES the record: a redactor exists to keep detail out of the log, so
// its failure must fall closed rather than echo an unredacted event.
func (p *producer) redact(
	ev observability.Fact,
) (observability.Fact, bool) {
	out, ok, r, stack := callHostHook(
		func() (observability.Fact, bool) { return p.redactor.RedactLog(ev) },
		p.redactorPanicked.Load() == 0)
	if r != nil {
		p.reportHookPanic("log redactor", &p.redactorPanicked, r, stack)

		return ev, false
	}

	return out, ok
}

// filtered applies the host's ObservationFilter for one recipient under
// containment. A panicking filter DENIES that recipient, for the same reason a
// panicking redactor suppresses: a policy that failed to run must not be
// treated as having permitted anything.
func (p *producer) filtered(
	obs Observer, ev observability.Fact,
) (observability.Fact, bool) {
	out, ok, r, stack := callHostHook(
		func() (observability.Fact, bool) {
			return p.filter.FilterObservation(obs, ev)
		},
		p.filterPanicked.Load() == 0)
	if r != nil {
		p.reportHookPanic("observation filter", &p.filterPanicked, r, stack)

		return ev, false
	}

	return out, ok
}

// callHostHook runs one host-supplied observability hook under panic
// containment. Both hooks are AuthorizationProvider methods called on the
// REPORTER's goroutine — an instance's loop — so an escaping panic there would
// take down the run of a process for a fault in the host's policy code. A
// broken policy must degrade its own event and nothing else.
//
// It mirrors deliver's shape: the recovered value and, for the first panic
// only, its stack come back to the caller, which owns counting and logging.
func callHostHook[T any](
	hook func() (T, bool), wantStack bool,
) (out T, ok bool, recovered any, stack []byte) {
	defer func() {
		if r := recover(); r != nil {
			recovered = r
			ok = false

			if wantStack {
				stack = debug.Stack()
			}
		}
	}()

	out, ok = hook()

	return out, ok, nil, nil
}

// reportHookPanic counts and logs one contained host-hook panic, bounded the
// way an observer panic is (ADR-022 v.1 §2.4): the FIRST one Warns with a
// stack — degraded but continuing, never Error, since no engine state was
// affected — and every one after it is a Debug, because the hot-path corollary
// forbids a per-event record above Debug.
func (p *producer) reportHookPanic(
	hook string, counter *atomic.Uint64, r any, stack []byte,
) {
	// the identity pair, plus the stack pair the Warn branch appends.
	args := make([]any, 0, 4)
	args = append(args, observability.AttrError, fmt.Sprint(r))

	// Add, not Load: two reporters can panic concurrently, and exactly one of
	// them must own the Warn.
	if counter.Add(1) != 1 {
		p.log.Debug(hook+" panicked", args...)

		return
	}

	p.log.Warn(hook+" panicked; its events are degraded until it is fixed",
		append(args, "stack", string(stack))...)
}

// fanout delivers ev to every engine-scope observer, applying the
// ObservationFilter per recipient (a denial is not a counted drop). The buffered
// send is non-blocking; a full buffer counts a drop. The lock spans the dispatch
// so unsubscribe can fence any in-flight send before closing a channel.
func (p *producer) fanout(ev observability.Fact) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, s := range p.subs {
		out := ev
		if p.filter != nil {
			filtered, ok := p.filtered(s.obs, ev)
			if !ok {
				continue // policy-denied — not a counted drop
			}

			out = filtered
		}

		select {
		case s.ch <- out:
		default:
			s.dropped.Add(1)
		}
	}
}

// subscribe registers o on the engine-scope stream and returns its Subscription,
// mirroring the instance handle's Observe: a buffered, lossy, drop-counted,
// panic-contained delivery drained by one goroutine.
func (p *producer) subscribe(o Observer) *Subscription {
	ch := make(chan observability.Fact, observerBuffer)
	done := make(chan struct{})

	var dropped, panicked atomic.Uint64

	go func() {
		defer close(done)

		for ev := range ch {
			deliverObserved(p.log, o, ev, &panicked)
		}
	}()

	p.mu.Lock()
	p.nextID++
	id := p.nextID
	p.subs[id] = &engineSub{ch: ch, done: done, dropped: &dropped, obs: o, id: id}
	p.mu.Unlock()

	var once sync.Once

	return &Subscription{
		dropped:  &dropped,
		panicked: &panicked,
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
