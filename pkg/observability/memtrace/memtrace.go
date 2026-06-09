// Package memtrace provides an opt-in observability.Tracer that retains the most
// recent completed spans in a bounded in-memory ring, queryable via Spans. It is
// a development and test aid; the engine's default tracer is the noop one, and
// production tracing is the adapters/otel module (ADR-002 §4.2).
package memtrace

import (
	"context"
	"sync"

	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// DefaultCapacity is the default number of recent spans retained.
const DefaultCapacity = 128

// Span is a completed span captured by the recorder.
type Span struct {
	Err        error
	Name       string
	StatusDesc string
	Attrs      []observability.Attr
	Status     observability.StatusCode
}

// Recorder is an in-memory observability.Tracer keeping the most recent spans.
type Recorder struct {
	ring []Span
	cap  int
	mu   sync.Mutex
}

// New returns a Recorder retaining up to capacity recent spans (DefaultCapacity
// if capacity <= 0).
func New(capacity int) *Recorder {
	if capacity <= 0 {
		capacity = DefaultCapacity
	}

	return &Recorder{cap: capacity, ring: make([]Span, 0, capacity)}
}

// Start begins a span; the returned context is unchanged.
func (r *Recorder) Start(
	ctx context.Context, name string, attrs ...observability.Attr,
) (context.Context, observability.Span) {
	return ctx, &liveSpan{rec: r, data: Span{Name: name, Attrs: attrs}}
}

// Spans returns a copy of the retained spans, oldest first.
func (r *Recorder) Spans() []Span {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]Span(nil), r.ring...)
}

func (r *Recorder) add(s Span) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.ring) == r.cap {
		copy(r.ring, r.ring[1:])
		r.ring[len(r.ring)-1] = s

		return
	}

	r.ring = append(r.ring, s)
}

type liveSpan struct {
	rec   *Recorder
	data  Span
	ended bool
}

func (s *liveSpan) End() {
	if s.ended {
		return
	}

	s.ended = true
	s.rec.add(s.data)
}

func (s *liveSpan) SetAttributes(attrs ...observability.Attr) {
	s.data.Attrs = append(s.data.Attrs, attrs...)
}

func (s *liveSpan) RecordError(err error) { s.data.Err = err }

func (s *liveSpan) SetStatus(code observability.StatusCode, desc string) {
	s.data.Status = code
	s.data.StatusDesc = desc
}

var (
	_ observability.Tracer = (*Recorder)(nil)
	_ observability.Span   = (*liveSpan)(nil)
)
