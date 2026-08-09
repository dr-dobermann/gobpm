package activities

import (
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

// rcvTaskConfig collects the ReceiveTask-specific options (those that don't
// belong to the embedded task) applied at NewReceiveTask.
type rcvTaskConfig struct {
	iterExpr    data.FormalExpression
	iterKeyName string
	instantiate bool
}

// Validate implements options.Configurator: an iteration-correlation
// declaration must carry BOTH halves (the validation rule — a half
// silently dropped would route nothing).
func (c *rcvTaskConfig) Validate() error {
	if (c.iterKeyName == "") != (c.iterExpr == nil) {
		return errs.New(
			errs.M("WithIterationCorrelation: both the key name and the "+
				"expression are required"),
			errs.C(errorClass, errs.InvalidParameter))
	}

	if strings.TrimSpace(c.iterKeyName) == "" && c.iterKeyName != "" {
		return errs.New(
			errs.M("WithIterationCorrelation: a blank key name isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	return nil
}

// RcvTaskOption is a ReceiveTask-specific construction option (e.g.
// WithInstantiate). NewReceiveTask separates these from the embedded task's
// options and applies them to the ReceiveTask itself. It does not return an
// error — its options only flip flags — while still satisfying options.Option
// via Apply (whose only failure is a wrong configurator type).
type RcvTaskOption func(*rcvTaskConfig)

// Option marks RcvTaskOption as an options.Option; NewReceiveTask applies it by
// calling the func directly.
func (RcvTaskOption) Option() {}

// WithIterationCorrelation declares how a concurrently-waiting
// iteration of this ReceiveTask (a parallel leaf Multi-Instance,
// SRD-086 FR-4) is addressed by an arriving message (ADR-006 v.5
// §2.9.3): keyName names a declared process CorrelationKey — its
// retrieval expressions derive the envelope-side value — and expr,
// evaluated at registration over the iteration's scope (where the
// split item is bound), produces the subscription-side value.
func WithIterationCorrelation(
	keyName string, expr data.FormalExpression,
) RcvTaskOption {
	return func(c *rcvTaskConfig) {
		c.iterKeyName = keyName
		c.iterExpr = expr
	}
}

// WithInstantiate marks the ReceiveTask as instantiating: a ReceiveTask with no
// incoming sequence flow and instantiate=true starts a new process instance on
// a matching message (BPMN §13.3.3), just like a message start event. It is the
// task-shaped peer of the message start event in the SRD-015 instantiation path.
func WithInstantiate() RcvTaskOption {
	return func(c *rcvTaskConfig) {
		c.instantiate = true
	}
}
