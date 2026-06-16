package thresher

// registerConfig holds the per-process registration choices applied by
// RegisterOption values at RegisterProcess (SRD-015). Its zero value is the
// default: auto-instantiation (each instantiating start trigger registers a
// persistent instance-starter).
type registerConfig struct {
	// manualStart, when set, suppresses auto event/message-driven
	// instantiation: no instance-starter is registered and the process is
	// instantiated only via StartProcess (SRD-015 FR-9, ADR-015 §2.2).
	manualStart bool
}

// RegisterOption tunes how a single process is registered with RegisterProcess.
// The default (no option) registers the process for auto-instantiation.
type RegisterOption func(*registerConfig) error

// WithManualStart registers a process as manual-start: the engine registers no
// persistent instance-starter for it, so no message ever spawns an instance —
// it is instantiated only via StartProcess (SRD-015 FR-9). Inside such a
// StartProcess-launched instance, its message-start nodes are seeded as ordinary
// in-instance catches (the intermediate-node rule), so the instance waits for
// its message after starting. This is an engine affordance (the default stays
// BPMN-conformant auto-instantiation) for tests — avoiding an instance-start
// storm off a shared broker — and for back-pressure control.
func WithManualStart() RegisterOption {
	return func(c *registerConfig) error {
		c.manualStart = true

		return nil
	}
}
