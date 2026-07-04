package options

// Configurator interface defines objects that can be configured and validated.
type Configurator interface {
	Validate() error
}

// Option is a compile-time marker for model configuration options. Options are
// applied by each constructor's type-switch calling the option's concrete func
// directly — there is no generic Apply. A new option type adds the marker method
// (func (X) Option() {}) alongside its underlying func; a new constructor
// dispatches by direct func call, never via an Apply(Configurator) round trip.
type Option interface {
	Option()
}
