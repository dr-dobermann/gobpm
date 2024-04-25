package thresher

import (
	"sync"

	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

type eventQueue struct {
	m sync.Mutex

	queue []flow.EventDefinition
}
