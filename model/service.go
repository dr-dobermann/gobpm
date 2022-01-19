package model

import (
	"context"

	mid "github.com/dr-dobermann/gobpm/internal/identity"
	"github.com/dr-dobermann/gobpm/model/base"
)

type Executor interface {
	Execute(ctx context.Context) Error
}

type Operation struct {
	base.BaseElement
	inMessageRef  mid.Id
	outMessageRef mid.Id
	errors        []mid.Id
	impl          Executor
}

type Interface struct {
	base.BaseElement
	name              string
	operations        []*Operation
	callabeElements   []*CallableElement
	implementationRef *interface{} // TODO: need to decide how to use this field
	// or just abandon it for Operation Executor
}
