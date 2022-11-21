package common

type BPMError interface {
	Name() string
	Code() string
}
