package base

import mid "github.com/dr-dobermann/gobpm/internal/identity"

type Documentation struct {
	Text   []byte
	Format string
}

type BaseElement struct {
	id mid.Id
	Documentation
}

func New(id mid.Id) *BaseElement {
	if id == mid.EmptyID() {
		id = mid.NewID()
	}

	return &BaseElement{id: id}
}

func (be *BaseElement) ID() mid.Id {
	return be.id
}

func (be *BaseElement) SetNewID(id mid.Id) {
	if id == mid.EmptyID() {
		id = mid.NewID()
	}

	be.id = id
}
