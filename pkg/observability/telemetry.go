package observability

import "context"

// Attr is a single telemetry attribute (a key/value pair). It mirrors the shape
// of OpenTelemetry's attribute.KeyValue without importing OTel, so the
// adapters/otel pass-through maps one to the other directly (ADR-002 §4.2).
type Attr struct {
	Value any
	Key   string
}

// StatusCode is a span's completion status, mirroring OpenTelemetry's span
// status codes.
type StatusCode int

const (
	// StatusUnset is the default, unset status.
	StatusUnset StatusCode = iota
	// StatusOK marks a span explicitly completed without error.
	StatusOK
	// StatusError marks a span that ended in error.
	StatusError
)

// Tracer starts tracing spans. It is modeled on OpenTelemetry's Tracer. The
// no-op default lives in the noop subpackage; an in-memory recent-spans ring in
// memtrace.
type Tracer interface {
	// Start begins a span and returns a context carrying it plus the Span to
	// end when the traced operation finishes.
	Start(ctx context.Context, name string, attrs ...Attr) (context.Context, Span)
}

// Span is an in-flight tracing span, modeled on OpenTelemetry's Span.
type Span interface {
	// End completes the span.
	End()
	// SetAttributes attaches attributes to the span.
	SetAttributes(attrs ...Attr)
	// RecordError records an error against the span.
	RecordError(err error)
	// SetStatus sets the span's completion status.
	SetStatus(code StatusCode, description string)
}

// MetricsRecorder creates metric instruments, modeled on OpenTelemetry's Meter.
// The default is the in-memory, series-capped registry in memmetrics; swap to
// the noop recorder to silence, or to adapters/otel for production.
type MetricsRecorder interface {
	// Counter returns a monotonic counter instrument by name.
	Counter(name string) Counter
	// Histogram returns a distribution instrument by name.
	Histogram(name string) Histogram
	// Gauge returns a last-value instrument by name.
	Gauge(name string) Gauge
}

// Counter is a monotonically increasing instrument.
type Counter interface {
	// Add increments the counter by value for the given attribute set.
	Add(ctx context.Context, value float64, attrs ...Attr)
}

// Histogram records a distribution of observed values.
type Histogram interface {
	// Record adds one observation for the given attribute set.
	Record(ctx context.Context, value float64, attrs ...Attr)
}

// Gauge records the latest value of a measurement.
type Gauge interface {
	// Set records value as the current reading for the given attribute set.
	Set(ctx context.Context, value float64, attrs ...Attr)
}
