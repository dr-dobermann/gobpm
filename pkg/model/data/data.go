package data

import (
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
)

// common error class for data package errors.
const errorClass = "DATA_ERRORS"

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

// indes is local helper function which returns index of item in items slice
// or -1 if slice doesn't found the item.
func index[T comparable](item T, slice []T) int {
	for i, it := range slice {
		if it == item {
			return i
		}
	}

	return -1
}
