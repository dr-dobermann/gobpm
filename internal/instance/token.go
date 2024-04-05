package instance

import "github.com/dr-dobermann/gobpm/pkg/model/foundation"

type TokenState uint8

const (
	TokenAlive TokenState = iota
	TokenDead
)

type token struct {
	foundation.ID

	inst  *Instance
	state TokenState
	prevs []*token
	nexts []*token
}
