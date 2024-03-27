package activities

import (
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

const errorClass = "ACTIVITIES_ERRORS"

// trim is a local helper function to trim spaces.
func trim(str string) string {
	return strings.Trim(str, " ")
}

// checkStr local helper function which checks if the str is empty string.
func checkStr(str, msg string) error {
	if str == "" {
		return errs.New(
			errs.M(msg),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	return nil
}

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
	_ flow.FlowNode = (*Activity)(nil)

	_ flow.SequenceSource = (*Activity)(nil)
	_ flow.SequenceTarget = (*Activity)(nil)
)
