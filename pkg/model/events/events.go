package events

import (
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

const errorClass = "EVENTS_ERRORS"

// map2slice returns slice of map items.
func map2slice[T any, I comparable](m map[I]T) []T {
	res := make([]T, 0, len(m))

	for _, v := range m {
		res = append(res, v)
	}

	return res
}

// interfaces check
var (
	_ flow.Node = (*StartEvent)(nil)
	_ flow.Node = (*EndEvent)(nil)

	_ flow.SequenceSource = (*StartEvent)(nil)
	_ flow.SequenceTarget = (*EndEvent)(nil)
)
