package options

type Option interface {
	Apply(cfg any) error
}
