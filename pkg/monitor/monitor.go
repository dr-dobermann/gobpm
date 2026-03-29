// Package monitor provides process monitoring and observability functionality.
package monitor

import (
	"time"
)

// CtxKey is a type for context keys used in monitoring.
type CtxKey string

// Key is the context key used to store monitor writer in context.
const Key CtxKey = "monitor_key"

// Event holds information about single monitoring Event.
type Event struct { //nolint:govet // field order matters for slog serialization
	Source  string
	Type    string
	At      time.Time
	Details map[string]any
}

// Writer is an interface of Monitor to write single
// monitoring Event.
type Writer interface {
	// Write saves single Event to Monitor.
	Write(*Event)
}

// WriterRegistrator interface defines objects that can register monitoring writers.
type WriterRegistrator interface {
	// RegisterWriter registers single monitoring events writer on
	// monitoring object.
	RegisterWriter(Writer)
}
