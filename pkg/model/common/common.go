package common

import (
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
)

const (
	errorClass = "COMMON_ERRORS"
)

// Strim is a local helper function to Strim spaces.
func Strim(str string) string {
	return strings.Trim(str, " ")
}

// CheckStr local helper function which checks if the str is empty string.
// If string is empty, then error returns with errMsg.
func CheckStr(str, errMsg string, errorClass string) error {
	if str == "" {
		return errs.New(
			errs.M(errMsg),
			errs.C(errorClass, errs.InvalidParameter))
	}

	return nil
}

// Map2List returns
func Map2List[T any, I comparable](m map[I]T) []T {
	if m == nil {
		return []T{}
	}

	res := make([]T, len(m))

	i := 0
	for _, v := range m {
		res[i] = v

		i++
	}

	return res
}
