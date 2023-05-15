package expression

type Condition interface {
	Check() bool
}
