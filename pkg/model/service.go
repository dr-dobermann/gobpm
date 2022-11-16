package model

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/foundation"
	mid "github.com/dr-dobermann/gobpm/pkg/identity"
)

type Executor interface {
	Execute(ctx context.Context) error
}

type Operation struct {
	foundation.BaseElement
	inMessageRef  mid.Id
	outMessageRef mid.Id
	errors        []mid.Id
	impl          Executor
}

type Interface struct {
	foundation.BaseElement
	name              string
	operations        []*Operation
	callabeElements   []*CallableElement
	implementationRef *interface{} // TODO: need to decide how to use this field
	// or just abandon it for Operation Executor
}
