package monitor

import (
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
