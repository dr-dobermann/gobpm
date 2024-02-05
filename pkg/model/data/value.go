package data

type Value interface {
	Get() (any, error)
	Update(any) error
}

type Collection interface {
	Len() int
	Rewind()
	GoTo(int) error
	Next() error
	GetAll() []any
}
