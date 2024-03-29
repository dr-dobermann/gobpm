package activities

import (
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

const errorClass = "ACTIVITIES_ERRORS"

// convertNilSlice returns empty slice if it gets nil slice and
// returns slice itself it it not nil.
func convertNilSlice[T any](slice []T) []T {
	if slice == nil {
		return []T{}
	}

	return slice
}

// interfaces check
var (
	_ flow.Node = (*Activity)(nil)

	_ flow.SequenceSource = (*Activity)(nil)
	_ flow.SequenceTarget = (*Activity)(nil)
)
