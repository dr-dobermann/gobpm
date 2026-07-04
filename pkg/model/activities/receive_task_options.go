package activities

// rcvTaskConfig collects the ReceiveTask-specific options (those that don't
// belong to the embedded task) applied at NewReceiveTask.
type rcvTaskConfig struct {
	instantiate bool
}

// Validate implements options.Configurator; rcvTaskConfig has no constraints.
func (*rcvTaskConfig) Validate() error {
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

// WithInstantiate marks the ReceiveTask as instantiating: a ReceiveTask with no
// incoming sequence flow and instantiate=true starts a new process instance on
// a matching message (BPMN §13.3.3), just like a message start event. It is the
// task-shaped peer of the message start event in the SRD-015 instantiation path.
func WithInstantiate() RcvTaskOption {
	return func(c *rcvTaskConfig) {
		c.instantiate = true
	}
}
