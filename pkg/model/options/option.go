package options

type Configurator interface {
	Validate() error
}
type Option interface {
	Apply(cfg Configurator) error
}
