package activities

// subProcessConfig collects the SubProcess-specific construction options.
type subProcessConfig struct {
	triggered     bool
	isTransaction bool
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

// WithTransaction marks the SubProcess as a Transaction Sub-Process (BPMN §10.7,
// ADR-028 §2.1): a plain embedded Sub-Process in every respect except that
// reaching a Cancel End Event inside it triggers an ACID-like abort — compensate
// its completed inner activities, terminate the running ones, and leave through
// its Cancel boundary. The marker only permits Cancel (End + boundary) and names
// the scope a cancel aborts; it is mutually exclusive with WithTriggeredByEvent
// (a handler is not a transaction).
func WithTransaction() SubProcessOption {
	return SubProcessOption(func(cfg *subProcessConfig) error {
		cfg.isTransaction = true

		return nil
	})
}
