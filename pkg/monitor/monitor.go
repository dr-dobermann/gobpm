package monitor

import (
	"log/slog"
	"time"
)

type CtxKey string

const Key CtxKey = "monitor_key"

// Event holds information about single monitoring Event.
type Event struct {
	Source  string
	Type    string
	At      time.Time
	Details map[string]any
}

// ------------- slog.LogValuer interface --------------------------------------

func (e *Event) LogValue() slog.Value {
	details := []slog.Attr{}

	if e.Source != "" {
		details = append(details, slog.String("source", e.Source))
	}

	if e.Type != "" {
		details = append(details, slog.String("type", e.Type))
	}

	details = append(details, slog.Time("at", e.At))

	for n, v := range e.Details {
		details = append(details, slog.Any(n, v))
	}

	return slog.GroupValue(details...)
}

// -----------------------------------------------------------------------------

// Writer is an interface of Monitor to write single
// monitoring Event.
type Writer interface {
	// Write saves single Event to Monitor.
	Write(*Event)
}

type WriterRegistrator interface {
	// RegisterWriter registers single monitoring events writer on
	// monitoring object.
	RegisterWriter(Writer)
}
