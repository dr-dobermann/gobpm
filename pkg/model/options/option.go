package options

const errorClass = "OPTIONS_ERRORS"

type Configurator interface {
	Validate() error
}

type Option interface {
	Apply(cfg Configurator) error
}
