package common

import (
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
)

const (
	errorClass = "COMMON_ERRORS"
)

// trim is a local helper function to trim spaces.
func trim(str string) string {
	return strings.Trim(str, " ")
}

// checkStr local helper function which checks if the str is empty string.
func checkStr(str, msg string) error {
	if str == "" {
		return errs.New(
			errs.M(msg),
			errs.C(errorClass, errs.InvalidParameter))
	}

	return nil
}
