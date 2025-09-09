package options

const errorClass = "OPTIONS_ERRORS"

// Configurator interface defines objects that can be configured and validated.
type Configurator interface {
	Validate() error
}

// Option interface defines configuration options that can be applied to Configurators.
type Option interface {
	Apply(cfg Configurator) error
}
