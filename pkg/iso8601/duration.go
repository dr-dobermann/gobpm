package iso8601

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// unit is one duration designator and what it is worth. Weeks and days are
// exact because ISO 8601 fixes them at 7 days and 24 hours; years and months
// are absent by design (see the package doc) and rejected explicitly below.
type unit struct {
	suffix byte
	span   time.Duration
}

var (
	dateUnits = []unit{{'W', 7 * 24 * time.Hour}, {'D', 24 * time.Hour}}
	timeUnits = []unit{{'H', time.Hour}, {'M', time.Minute}, {'S', time.Second}}
)

// ParseDuration parses an ISO 8601 duration into a time.Duration.
//
// Accepted: PnW (exclusive — a week form carries nothing else), PnD, PTnHnMnS,
// and the combined P1DT2H3M4S. Rejected: the calendar designators Y and M in
// the date part, fractional components, a non-positive total, and any
// malformed input — each with an error naming the reason.
func ParseDuration(s string) (time.Duration, error) {
	body, ok := strings.CutPrefix(s, "P")
	if !ok || body == "" {
		return 0, fmt.Errorf(
			"iso8601: %q is not a duration — it must start with P "+
				"(e.g. P10D, PT10H, P1DT2H)", s)
	}

	// body is non-empty here, so a T-less body always yields a non-empty
	// datePart — "carries nothing" is already refused by the prefix guard.
	datePart, timePart, hasT := strings.Cut(body, "T")

	if hasT && timePart == "" {
		return 0, fmt.Errorf(
			"iso8601: %q has a T with no time components after it", s)
	}

	total, err := parseDatePart(s, datePart)
	if err != nil {
		return 0, err
	}

	timeSpan, err := parseSection(s, timePart, timeUnits, "time")
	if err != nil {
		return 0, err
	}

	total += timeSpan

	if total <= 0 {
		return 0, fmt.Errorf(
			"iso8601: %q is a zero-length duration — a timer that fires "+
				"after no time is not a timer", s)
	}

	return total, nil
}

// parseDatePart handles the pre-T section, where the week form is exclusive and
// the calendar designators are refused.
func parseDatePart(src, datePart string) (time.Duration, error) {
	if datePart == "" {
		return 0, nil
	}

	if i := strings.IndexAny(datePart, "YM"); i >= 0 {
		return 0, fmt.Errorf(
			"iso8601: %q uses the %c designator, which has no fixed length "+
				"— a month is 28 to 31 days and a year 365 or 366, so it "+
				"cannot become a duration; use days or weeks",
			src, datePart[i])
	}

	if strings.ContainsRune(datePart, 'W') && datePart[len(datePart)-1] != 'W' {
		return 0, fmt.Errorf(
			"iso8601: %q combines W with other fields — ISO 8601 makes the "+
				"week form exclusive (P2W, not P1W2D)", src)
	}

	return parseSection(src, datePart, dateUnits, "date")
}

// parseSection consumes "<number><suffix>" pairs in the order units lists them,
// so a repeated or out-of-order designator fails rather than being tolerated.
func parseSection(
	src, section string, units []unit, which string,
) (time.Duration, error) {
	var total time.Duration

	rest := section
	for _, u := range units {
		before, after, found := strings.Cut(rest, string(u.suffix))
		if !found {
			continue
		}

		n, err := parseCount(src, before, u.suffix)
		if err != nil {
			return 0, err
		}

		total += time.Duration(n) * u.span
		rest = after
	}

	if rest != "" {
		return 0, fmt.Errorf(
			"iso8601: %q has unparsed %s components %q — designators must be "+
				"uppercase and in order", src, which, rest)
	}

	return total, nil
}

// parseCount reads the integer preceding a designator, refusing a fraction
// rather than truncating it.
func parseCount(src, digits string, suffix byte) (int64, error) {
	if strings.ContainsRune(digits, '.') || strings.ContainsRune(digits, ',') {
		return 0, fmt.Errorf(
			"iso8601: %q uses a fractional %c component, which this parser "+
				"refuses rather than truncate", src, suffix)
	}

	n, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return 0, fmt.Errorf(
			"iso8601: %q has no valid number before %c", src, suffix)
	}

	if n < 0 {
		return 0, fmt.Errorf(
			"iso8601: %q has a negative %c component", src, suffix)
	}

	return n, nil
}
