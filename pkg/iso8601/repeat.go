package iso8601

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Repeat is a parsed ISO 8601 bounded repeating interval — the form BPMN's
// timeCycle carries (Table 10.101). The engine stores the two fields
// separately, as a repetition count beside its interval, so this is exactly the
// shape a TimerEventDefinition consumes.
type Repeat struct {
	// Interval is the gap between firings.
	Interval time.Duration

	// Count is how many times the timer fires. Always >= 1: an unbounded
	// recurrence is rejected at parse.
	Count int
}

// ParseRepeat parses a BOUNDED ISO 8601 repeating interval, Rn/<duration>.
//
// Accepted: R3/PT10H, R1/P1D — a count of at least one, then a duration this
// package's ParseDuration accepts. Rejected: the unbounded R/<duration>, a
// zero count, and anything malformed.
func ParseRepeat(s string) (Repeat, error) {
	body, ok := strings.CutPrefix(s, "R")
	if !ok {
		return Repeat{}, fmt.Errorf(
			"iso8601: %q is not a repeating interval — it must start with R "+
				"(e.g. R3/PT10H)", s)
	}

	digits, durPart, found := strings.Cut(body, "/")
	if !found {
		return Repeat{}, fmt.Errorf(
			"iso8601: %q has no / separating the count from the interval "+
				"(e.g. R3/PT10H)", s)
	}

	if digits == "" {
		return Repeat{}, fmt.Errorf(
			"iso8601: %q is an UNBOUNDED recurrence, which the engine does "+
				"not support — no element can consume one safely, so give a "+
				"count (e.g. R3/%s)", s, durPart)
	}

	count, err := strconv.Atoi(digits)
	if err != nil {
		return Repeat{}, fmt.Errorf(
			"iso8601: %q has no valid repetition count before /", s)
	}

	if count < 1 {
		return Repeat{}, fmt.Errorf(
			"iso8601: %q repeats %d times — a recurrence must fire at least "+
				"once", s, count)
	}

	interval, err := ParseDuration(durPart)
	if err != nil {
		return Repeat{}, fmt.Errorf("iso8601: %q: %w", s, err)
	}

	return Repeat{Count: count, Interval: interval}, nil
}
