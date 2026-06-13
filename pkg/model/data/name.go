package data

import (
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
)

// PathSeparator is the reserved data-path separator. It must not appear in a
// data element's name so a path-qualified read ("SOURCE/addr") splits
// unambiguously on its first occurrence (ADR-010 v.2 §2.7).
const PathSeparator = "/"

// CheckName reports a classified error if name contains the reserved
// PathSeparator. Data-element constructors call it to keep '/' available as
// the data-plane path separator; errorClass identifies the calling element.
func CheckName(name, errorClass string) error {
	if strings.Contains(name, PathSeparator) {
		return errs.New(
			errs.M("data name %q must not contain the reserved separator %q",
				name, PathSeparator),
			errs.C(errorClass, errs.InvalidParameter))
	}

	return nil
}
