package convert

import "fmt"

// UnsupportedElementError reports an element of the source format that the
// converter does not map into the executable-core subset — the SAD-001 §5
// "clear feedback on unsupported elements" requirement (SRD-051 §FR-3).
//
// It is returned from Import (and Export) when the converter meets an
// in-scope element it does not support; out-of-scope foreign-namespace
// content (diagram interchange etc.) is skipped silently instead.
type UnsupportedElementError struct {
	Tag     string // local element name, e.g. "inclusiveGateway"
	ID      string // the element's id attr, if present
	Section string // spec §, e.g. "§13.4.3"; empty when unknown
}

// Error returns a human-readable description of the unsupported element.
func (e *UnsupportedElementError) Error() string {
	s := fmt.Sprintf("unsupported element %q", e.Tag)

	if e.ID != "" {
		s += fmt.Sprintf(" (id %q)", e.ID)
	}

	if e.Section != "" {
		s += fmt.Sprintf(", spec %s", e.Section)
	}

	return s
}
