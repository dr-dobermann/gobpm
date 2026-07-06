package activities

import "time"

// srvTaskConfig collects the ServiceTask-specific options (those that don't
// belong to the embedded task) applied at NewServiceTask. It is extended by the
// external-worker options in later SRDs (SRD-036..038).
type srvTaskConfig struct {
	timeout time.Duration
}

// SrvTaskOption is a ServiceTask-specific construction option (e.g.
// WithTimeout). NewServiceTask separates these from the embedded task's options
// and applies them to the ServiceTask itself.
type SrvTaskOption func(*srvTaskConfig)

// Option marks SrvTaskOption as an options.Option; NewServiceTask applies it by
// calling the func directly.
func (SrvTaskOption) Option() {}

// WithTimeout bounds the in-process Operation execution to d and makes it
// context-cancellable (ADR-021 v.1 §2.9, SRD-035). When d is positive, Exec
// runs the Operation in a sub-goroutine and returns as soon as the operation
// finishes, the context is canceled (a boundary interrupt or an instance
// abort), or d elapses — whichever comes first; a timeout faults the task.
//
// A non-positive d (the default) means no bound: the operation runs
// synchronously to completion, exactly as before.
//
// NOTE: the bound protects the ENGINE, not the operation — Go cannot terminate
// a goroutine. An operation that ignores its context keeps running in a leaked
// goroutine after a timeout; confine an operation's effects to its returned
// value (the engine binds only that), and honor the context for true
// cancellation.
func WithTimeout(d time.Duration) SrvTaskOption {
	return func(c *srvTaskConfig) {
		c.timeout = d
	}
}
