package data

import (
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
)

// PathSeparator is the reserved data-path separator. It must not appear in a
// data element's name so a path-qualified read ("SOURCE/addr") splits
// unambiguously on its first occurrence (ADR-010 v.2 §2.7).
const PathSeparator = "/"

// reservedNameChars are the characters a data element's name must not
// contain: the provider separator '/' (ADR-010 v.2 §2.7) and the structural
// path characters '.', '[', ']' (ADR-011 v.6 §2.9.2, SRD-042 FR-6) — so a
// structural path ("order.items[0].price") is never ambiguous with a name.
const reservedNameChars = PathSeparator + ".[]"

// CheckName reports a classified error if name contains a reserved character
// (see reservedNameChars). Data-element constructors call it to keep the
// path vocabulary unambiguous; errorClass identifies the calling element.
func CheckName(name, errorClass string) error {
	if i := strings.IndexAny(name, reservedNameChars); i >= 0 {
		return errs.New(
			errs.M("data name %q must not contain the reserved character %q",
				name, string(name[i])),
			errs.C(errorClass, errs.InvalidParameter))
	}

	return nil
}
