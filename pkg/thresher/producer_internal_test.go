package thresher

import (
	"strings"
	"sync"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/auth"
	"github.com/dr-dobermann/gobpm/pkg/auth/allowall"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/stretchr/testify/require"
)

// recLogger records the leveled echo calls.
type recLogger struct {
	mu   sync.Mutex
	msgs []string
}

func (r *recLogger) put(msg string) {
	r.mu.Lock()
	r.msgs = append(r.msgs, msg)
	r.mu.Unlock()
}

func (r *recLogger) Debug(msg string, _ ...any) { r.put("DEBUG:" + msg) }
func (r *recLogger) Info(msg string, _ ...any)  { r.put("INFO:" + msg) }
func (r *recLogger) Warn(msg string, _ ...any)  { r.put("WARN:" + msg) }
func (r *recLogger) Error(msg string, _ ...any) { r.put("ERROR:" + msg) }

func (r *recLogger) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return len(r.msgs)
}

// countWith returns how many recorded records contain sub — used to separate
// one specific record from the ordinary echo traffic sharing the logger.
func (r *recLogger) countWith(sub string) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	n := 0

	for _, m := range r.msgs {
		if strings.Contains(m, sub) {
			n++
		}
	}

	return n
}

// capAuthz is an allow-all authorizer that also carries the two optional
// visibility capabilities, letting one double drive both channels.
type capAuthz struct {
	auth.AuthorizationProvider
	suppressLog bool
	denyObserve bool
	panicLog    bool
	panicObs    bool
}

func (a capAuthz) RedactLog(ev observability.Fact) (observability.Fact, bool) {
	if a.panicLog {
		panic("the host's redactor is broken")
	}

	return ev, !a.suppressLog
}

func (a capAuthz) FilterObservation(_ any, ev observability.Fact) (observability.Fact, bool) {
	if a.panicObs {
		panic("the host's filter is broken")
	}

	return ev, !a.denyObserve
}

// recObserver records delivered Facts.
type recObserver struct {
	mu  sync.Mutex
	got []observability.Fact
}

func (o *recObserver) OnFact(f observability.Fact) {
	o.mu.Lock()
	o.got = append(o.got, f)
	o.mu.Unlock()
}

func (o *recObserver) count() int {
	o.mu.Lock()
	defer o.mu.Unlock()

	return len(o.got)
}

func lifecycleEvent() observability.Fact {
	return observability.Fact{
		Kind:  observability.KindInstanceState,
		Phase: observability.PhaseActive,
		Details: map[string]string{
			observability.AttrInstanceID: "inst-1",
		},
	}
}

// TestProducerEchoesWithNoObservers (T-3/T-9): with no engine observers, Report
// still writes the operator-log echo (visible-by-default) and delivers to no one.
func TestProducerEchoesWithNoObservers(t *testing.T) {
	log := &recLogger{}
	p := newProducer(log, allowall.New())

	p.Report(lifecycleEvent())

	require.Equal(t, 1, log.count(), "the echo fires even with no observers")
}

// TestProducerDataChangeDoesNotEcho (T-3): a stream-only kind writes no echo.
func TestProducerDataChangeDoesNotEcho(t *testing.T) {
	log := &recLogger{}
	p := newProducer(log, allowall.New())

	p.Report(observability.Fact{
		Kind:  observability.KindDataChange,
		Phase: observability.PhaseValueUpdated,
	})

	require.Zero(t, log.count(), "DataChange is observer-stream only")
}

// TestProducerFanoutDelivers (T-2): a subscribed observer receives the canonical
// Fact — kind, phase, and instance_id in Details.
func TestProducerFanoutDelivers(t *testing.T) {
	p := newProducer(&recLogger{}, allowall.New())

	o := &recObserver{}
	sub := p.subscribe(o)

	p.Report(lifecycleEvent())

	sub.Cancel() // drains

	require.Equal(t, 1, o.count())
	o.mu.Lock()
	f := o.got[0]
	o.mu.Unlock()
	require.Equal(t, observability.KindInstanceState, f.Kind)
	require.Equal(t, observability.PhaseActive, f.Phase)
	require.Equal(t, "inst-1", f.Details[observability.AttrInstanceID])
}

// TestProducerLogRedactorSuppresses (T-8): a suppressing LogRedactor drops the
// echo but leaves the observer stream intact.
func TestProducerLogRedactorSuppresses(t *testing.T) {
	log := &recLogger{}
	p := newProducer(log, capAuthz{
		AuthorizationProvider: allowall.New(),
		suppressLog:           true,
	})

	o := &recObserver{}
	sub := p.subscribe(o)

	p.Report(lifecycleEvent())

	sub.Cancel()

	require.Zero(t, log.count(), "redactor suppressed the echo")
	require.Equal(t, 1, o.count(), "the observer stream is unaffected")
}

// TestProducerObservationFilterDenies (T-8): a denying ObservationFilter blocks
// delivery without counting a drop; the echo still fires.
func TestProducerObservationFilterDenies(t *testing.T) {
	log := &recLogger{}
	p := newProducer(log, capAuthz{
		AuthorizationProvider: allowall.New(),
		denyObserve:           true,
	})

	o := &recObserver{}
	sub := p.subscribe(o)

	p.Report(lifecycleEvent())

	require.Equal(t, 1, log.count(), "the echo is not affected by the observer filter")
	require.Zero(t, o.count(), "the filter denied delivery")
	require.Zero(t, sub.Dropped(), "a policy denial is not a counted drop")

	sub.Cancel()
}

// TestProducerDropsWhenBufferFull (T-2 drop semantics): a stalled observer past
// the buffer counts drops rather than blocking the producer.
func TestProducerDropsWhenBufferFull(t *testing.T) {
	p := newProducer(&recLogger{}, allowall.New())

	release := make(chan struct{})
	sub := p.subscribe(&blockingRecObserver{release: release})

	// One event unblocks into the drain (which then blocks); the rest overflow
	// the buffer and are dropped.
	for range observerBuffer + 10 {
		p.Report(lifecycleEvent())
	}

	require.Positive(t, sub.Dropped())

	close(release)
	sub.Cancel()
}

// blockingRecObserver blocks in OnFact until release is closed.
type blockingRecObserver struct{ release chan struct{} }

func (b *blockingRecObserver) OnFact(observability.Fact) { <-b.release }

// TestPanickingObservationFilterContained is FIX-036 T-5: the ObservationFilter
// is host code called on the REPORTER's goroutine — an instance's execution
// loop — so a panic in it used to propagate out of Report and kill the run of a
// process for a fault in the host's policy. It is now contained: the recipient
// is denied (a policy that failed to run has not permitted anything), the panic
// is counted, and it is logged once at Warn rather than per event.
func TestPanickingObservationFilterContained(t *testing.T) {
	log := &recLogger{}
	p := newProducer(log, capAuthz{
		AuthorizationProvider: allowall.New(),
		panicObs:              true,
	})

	o := &recObserver{}
	sub := p.subscribe(o)

	require.NotPanics(t, func() {
		p.Report(lifecycleEvent())
		p.Report(lifecycleEvent())
	}, "a broken filter must not reach Report's caller")

	sub.Cancel()

	require.Zero(t, o.count(),
		"a filter that failed to run denies its recipient")
	require.Zero(t, sub.Dropped(),
		"a contained panic is a policy denial, not a buffer drop")
	require.Equal(t, uint64(2), p.filterPanicked.Load(),
		"every panic is counted")
	require.Equal(t, 1, log.countWith("WARN:observation filter panicked"),
		"the Warn is one per engine, not one per event")
	require.Equal(t, 1, log.countWith("DEBUG:observation filter panicked"),
		"later panics stay at Debug — the hot path carries no Warn")
}

// TestPanickingLogRedactorContained is T-5's other half: RedactLog is the same
// kind of host code on the same goroutine. A panic suppresses the record — a
// redactor exists to keep detail OUT of the log, so its failure must not be
// read as permission to echo the event unredacted — and the observer stream,
// which the redactor has no say over, is unaffected.
func TestPanickingLogRedactorContained(t *testing.T) {
	log := &recLogger{}
	p := newProducer(log, capAuthz{
		AuthorizationProvider: allowall.New(),
		panicLog:              true,
	})

	o := &recObserver{}
	sub := p.subscribe(o)

	require.NotPanics(t, func() { p.Report(lifecycleEvent()) },
		"a broken redactor must not reach Report's caller")

	sub.Cancel()

	require.Equal(t, uint64(1), p.redactorPanicked.Load())
	require.Equal(t, 1, log.countWith("WARN:log redactor panicked"))
	require.Equal(t, 1, log.count(),
		"only the Warn — the unredacted record is suppressed, not echoed")
	require.Equal(t, 1, o.count(),
		"the observer stream is not the redactor's to deny")
}
