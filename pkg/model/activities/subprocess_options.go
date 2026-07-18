package activities

// subProcessConfig collects the SubProcess-specific construction options.
type subProcessConfig struct {
	triggered bool
}

// SubProcessOption is a SubProcess-specific construction option. NewSubProcess
// separates these from the embedded activity's options and applies them to the
// SubProcess itself.
type SubProcessOption func(*subProcessConfig) error

// Option marks SubProcessOption as an options.Option; NewSubProcess applies it
// by calling the func directly.
func (SubProcessOption) Option() {}

// WithTriggeredByEvent marks the SubProcess as an Event Sub-Process (BPMN
// §13.5.4, ADR-023 v.2 §2.10): a handler armed while its enclosing scope is
// open, entered only when its single triggered Start Event fires — not by a
// sequence flow. Its inner graph must then have exactly one interrupting
// triggered start (Message/Timer/Signal/Error/Conditional) instead of the
// embedded Sub-Process's None-start / flow-less entry (SRD-052).
func WithTriggeredByEvent() SubProcessOption {
	return SubProcessOption(func(cfg *subProcessConfig) error {
		cfg.triggered = true

		return nil
	})
}
