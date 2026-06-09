// Package noop provides no-op observability implementations: a Tracer that
// creates inert spans (the engine's default tracer) and a MetricsRecorder that
// discards all measurements (the opt-out for metrics). Both are allocation-free
// on the hot path.
package noop

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// Tracer is a no-op observability.Tracer.
type Tracer struct{}

// NewTracer returns a no-op Tracer.
func NewTracer() observability.Tracer { return Tracer{} }

// Start returns the context unchanged and a no-op span.
func (Tracer) Start(
	ctx context.Context, _ string, _ ...observability.Attr,
) (context.Context, observability.Span) {
	return ctx, span{}
}

type span struct{}

func (span) End()                                           {}
func (span) SetAttributes(_ ...observability.Attr)          {}
func (span) RecordError(_ error)                            {}
func (span) SetStatus(_ observability.StatusCode, _ string) {}

// MetricsRecorder is a no-op observability.MetricsRecorder.
type MetricsRecorder struct{}

// NewMetricsRecorder returns a no-op MetricsRecorder.
func NewMetricsRecorder() observability.MetricsRecorder { return MetricsRecorder{} }

// Counter returns a no-op counter instrument.
func (MetricsRecorder) Counter(_ string) observability.Counter { return instrument{} }

// Histogram returns a no-op histogram instrument.
func (MetricsRecorder) Histogram(_ string) observability.Histogram { return instrument{} }

// Gauge returns a no-op gauge instrument.
func (MetricsRecorder) Gauge(_ string) observability.Gauge { return instrument{} }

// instrument is the shared no-op for all three instrument kinds.
type instrument struct{}

func (instrument) Add(context.Context, float64, ...observability.Attr)    {}
func (instrument) Record(context.Context, float64, ...observability.Attr) {}
func (instrument) Set(context.Context, float64, ...observability.Attr)    {}

var (
	_ observability.Tracer          = Tracer{}
	_ observability.Span            = span{}
	_ observability.MetricsRecorder = MetricsRecorder{}
	_ observability.Counter         = instrument{}
	_ observability.Histogram       = instrument{}
	_ observability.Gauge           = instrument{}
)
