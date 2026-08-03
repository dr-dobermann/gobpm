package iso8601

import (
	"fmt"
	"time"
)

// dateOnly is the calendar-date profile ISO 8601 allows where a time of day is
// not given. RFC 3339 requires the time, so it is accepted separately and read
// as midnight UTC.
const dateOnly = "2006-01-02"

// ParseDateTime parses an ISO 8601 date-time into a time.Time — the form BPMN's
// timeDate carries (Table 10.101).
//
// Accepted: the RFC 3339 profile with an offset or Z (2011-03-11T12:13:14Z,
// 2026-08-03T15:00:00+05:00) and a bare calendar date (2011-03-11), read as
// midnight UTC.
func ParseDateTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, fmt.Errorf("iso8601: empty date-time")
	}

	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}

	if t, err := time.Parse(dateOnly, s); err == nil {
		return t, nil
	}

	return time.Time{}, fmt.Errorf(
		"iso8601: %q is not a date-time — expected 2006-01-02T15:04:05Z07:00 "+
			"or 2006-01-02", s)
}
