package activities

import (
	"strings"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/tasks"
)

// srvTaskConfig collects the ServiceTask-specific options (those that don't
// belong to the embedded task) applied at NewServiceTask. It is extended by the
// external-worker options (SRD-036/037/038).
type srvTaskConfig struct {
	errorMapper     tasks.ErrorMapper
	retryPolicy     tasks.RetryPolicy
	workerTopic     tasks.Topic
	statusVar       string
	outputMapping   []tasks.OutputRule
	timeout         time.Duration
	trustMode       tasks.TrustMode
	statusOverwrite bool
	trustSet        bool
}

// SrvTaskOption is a ServiceTask-specific construction option (e.g. WithTimeout).
// NewServiceTask separates these from the embedded task's options and applies
// them to the ServiceTask itself; a bad option value is rejected with an error.
type SrvTaskOption func(*srvTaskConfig) error

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
	return func(c *srvTaskConfig) error {
		c.timeout = d

		return nil
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
	return func(c *srvTaskConfig) error {
		c.workerTopic = tasks.Topic(topic)

		return nil
	}
}

// WithErrorMapper sets the per-service ErrorMapper that classifies a worker's raw
// fault into a Business Error / Business Status / technical outcome (ADR-021 §2.6,
// SRD-037). A nil mapper is rejected. Governs the worker outcome, so it is valid
// only on a worker-dispatched ServiceTask (checked at NewServiceTask).
func WithErrorMapper(m tasks.ErrorMapper) SrvTaskOption {
	return func(c *srvTaskConfig) error {
		if m == nil {
			return errs.New(
				errs.M("WithErrorMapper: a nil ErrorMapper isn't allowed"),
				errs.C(errorClass, errs.EmptyNotAllowed))
		}

		c.errorMapper = m

		return nil
	}
}

// WithRetryPolicy sets the per-service RetryPolicy that governs technical-fault
// retries for the worker-dispatched task (ADR-021 §2.7, SRD-038). A nil policy is
// rejected. Overrides the engine-wide WithWorkerRetryPolicy default; absent both,
// the engine's DefaultRetryPolicy applies. Valid only on a worker-dispatched
// ServiceTask (checked at NewServiceTask).
func WithRetryPolicy(p tasks.RetryPolicy) SrvTaskOption {
	return func(c *srvTaskConfig) error {
		if p == nil {
			return errs.New(
				errs.M("WithRetryPolicy: a nil RetryPolicy isn't allowed"),
				errs.C(errorClass, errs.EmptyNotAllowed))
		}

		c.retryPolicy = p

		return nil
	}
}

// WithWorkerTrust sets the per-service trust mode — where the worker outcome's
// policy bundle (output mapping, classification, retry) executes: WorkerTrusted
// (the worker) or EngineAuthoritative (the engine's dispatcher) (ADR-021 §2.6,
// SRD-039). An invalid mode is rejected. Overrides the engine-wide
// WithWorkerTrustDefault; absent both, WorkerTrusted (the ADR default) applies.
// Valid only on a worker-dispatched ServiceTask (checked at NewServiceTask).
func WithWorkerTrust(mode tasks.TrustMode) SrvTaskOption {
	return func(c *srvTaskConfig) error {
		if mode != tasks.WorkerTrusted && mode != tasks.EngineAuthoritative {
			return errs.New(
				errs.M("WithWorkerTrust: unknown trust mode %q", mode.String()),
				errs.C(errorClass, errs.InvalidParameter))
		}

		c.trustMode = mode
		c.trustSet = true

		return nil
	}
}

// WithStatus names the task-scoped variable a Business Status outcome writes, and
// whether it may overwrite an existing one (ADR-021 §2.6, SRD-037 FR-5). An empty
// name is rejected. overwrite=false makes a pre-existing variable a runtime
// collision fault (no silent clobber). Valid only on a worker-dispatched
// ServiceTask (checked at NewServiceTask).
func WithStatus(statusName string, overwrite bool) SrvTaskOption {
	return func(c *srvTaskConfig) error {
		if strings.TrimSpace(statusName) == "" {
			return errs.New(
				errs.M("WithStatus: an empty status variable name isn't allowed"),
				errs.C(errorClass, errs.EmptyNotAllowed))
		}

		c.statusVar = statusName
		c.statusOverwrite = overwrite

		return nil
	}
}

// WithOutputMapping shapes a worker's raw Complete response body into the
// ServiceTask's output via {body-path → output variable} rules (ADR-021 §2.5,
// SRD-037 FR-7). Absent a mapping, the Complete payload is taken as the output
// directly. Each rule needs a non-nil Path and a non-empty Var. Valid only on a
// worker-dispatched ServiceTask (checked at NewServiceTask).
func WithOutputMapping(rules ...tasks.OutputRule) SrvTaskOption {
	return func(c *srvTaskConfig) error {
		for i, r := range rules {
			if r.Path == nil {
				return errs.New(
					errs.M("WithOutputMapping: rule %d has a nil Path", i),
					errs.C(errorClass, errs.EmptyNotAllowed))
			}

			if strings.TrimSpace(r.Var) == "" {
				return errs.New(
					errs.M("WithOutputMapping: rule %d has an empty Var", i),
					errs.C(errorClass, errs.EmptyNotAllowed))
			}
		}

		c.outputMapping = rules

		return nil
	}
}
