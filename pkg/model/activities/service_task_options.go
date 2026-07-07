package activities

import (
	"time"

	"github.com/dr-dobermann/gobpm/pkg/tasks"
)

// srvTaskConfig collects the ServiceTask-specific options (those that don't
// belong to the embedded task) applied at NewServiceTask. It is extended by the
// external-worker options in later SRDs (SRD-037/038).
type srvTaskConfig struct {
	workerTopic tasks.Topic
	timeout     time.Duration
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

// WithWorker makes the ServiceTask an EXTERNAL-worker wait node (ADR-021 §2.1,
// SRD-036): instead of running its Operation in-process, the engine enqueues a
// job on topic and parks the task until a worker fetches, executes, and reports
// the outcome. Valid only on a message-operation ServiceTask — a Go operation
// (an in-process closure) can't be shipped to a worker, so combining WithWorker
// with a Go operation is a build-time error (§2.3). An empty topic is a no-op
// (the task stays in-process).
func WithWorker(topic string) SrvTaskOption {
	return func(c *srvTaskConfig) {
		c.workerTopic = tasks.Topic(topic)
	}
}
