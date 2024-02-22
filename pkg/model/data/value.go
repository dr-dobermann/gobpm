package data

type Value interface {
	Get() any
	Update(any) error
}

type Collection interface {
	Len() int
	Rewind()
	GoTo(int) error
	Next() error
	GetAll() []any
	Index() int
	Clear() error
}
