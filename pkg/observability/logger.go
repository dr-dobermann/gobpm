// Package observability defines gobpm's structured-logging and telemetry
// contracts: the Logger interface (satisfied directly by *slog.Logger) and the
// OpenTelemetry-shaped Tracer / MetricsRecorder interfaces. The interfaces live
// here; their default implementations live in sibling subpackages (noop,
// memmetrics, memtrace). Core never imports OpenTelemetry — the real OTel types
// live only in the adapters/otel module (ADR-002 §4.2).
package observability

import "log/slog"

// Logger is the engine's structured-logging contract. It is intentionally the
// leveled subset of *slog.Logger, so the standard library's *slog.Logger
// satisfies it directly (the default is slog.Default()), while non-slog loggers
// remain pluggable.
type Logger interface {
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

// *slog.Logger must always satisfy Logger — the interface is defined as the
// slog-compatible subset on purpose (ADR-002 §4.2).
var _ Logger = (*slog.Logger)(nil)
