package iso8601_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/iso8601"
)

// TestParseDuration covers the accepted duration forms (SRD-077 T-3, FR-4).
func TestParseDuration(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want time.Duration
	}{
		{"P10D", 10 * 24 * time.Hour},
		{"P1D", 24 * time.Hour},
		{"P2W", 14 * 24 * time.Hour},
		{"PT10H", 10 * time.Hour},
		{"PT5M", 5 * time.Minute},
		{"PT30S", 30 * time.Second},
		{"PT1H30M", time.Hour + 30*time.Minute},
		{"P1DT2H3M4S", 24*time.Hour + 2*time.Hour + 3*time.Minute + 4*time.Second},
		{"PT0H1S", time.Second},
	} {
		t.Run(tc.in, func(t *testing.T) {
			got, err := iso8601.ParseDuration(tc.in)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

// TestParseDurationRejects proves each refusal NAMES its reason, which is the
// requirement — not merely that the call fails (SRD-077 T-3, FR-5).
func TestParseDurationRejects(t *testing.T) {
	for _, tc := range []struct {
		name, in, wantMsg string
	}{
		{"year designator", "P1Y", "no fixed length"},
		{"month designator", "P1M", "no fixed length"},
		{"year inside a combination", "P1Y2M3DT4H", "no fixed length"},
		{"week combined with days", "P1W2D", "week form exclusive"},
		{"fractional seconds", "PT0.5S", "fractional"},
		{"zero days", "P0D", "zero-length duration"},
		{"zero seconds", "PT0S", "zero-length duration"},
		{"lowercase", "p10d", "must start with P"},
		{"missing P", "10D", "must start with P"},
		{"bare P", "P", "must start with P"},
		{"T with no time part", "P1DT", "T with no time components"},
		{"negative", "P-1D", "negative D component"},
		{"garbage", "PXZQ", "unparsed"},
		{"letters where a number belongs", "PXD", "no valid number before D"},
		{"letters before a time designator", "PTXH", "no valid number before H"},
		// A calendar designator inside garbage is reported as such: the Y/M
		// check runs first, and naming it is more useful than "unparsed".
		{"garbage containing Y", "PXYZ", "no fixed length"},
		{"empty", "", "must start with P"},
		{"go syntax", "10h", "must start with P"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := iso8601.ParseDuration(tc.in)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantMsg,
				"the error must say WHY %q was refused", tc.in)
		})
	}
}

// TestParseDateTime covers timeDate's accepted spellings (SRD-077 T-4).
func TestParseDateTime(t *testing.T) {
	got, err := iso8601.ParseDateTime("2011-03-11T12:13:14Z")
	require.NoError(t, err)
	require.Equal(t, time.Date(2011, 3, 11, 12, 13, 14, 0, time.UTC), got.UTC())

	offset, err := iso8601.ParseDateTime("2026-08-03T15:00:00+05:00")
	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC), offset.UTC())

	dateOnly, err := iso8601.ParseDateTime("2011-03-11")
	require.NoError(t, err)
	require.Equal(t, time.Date(2011, 3, 11, 0, 0, 0, 0, time.UTC), dateOnly.UTC())

	for _, bad := range []string{"", "11/03/2011", "2011-13-45", "tomorrow"} {
		_, err := iso8601.ParseDateTime(bad)
		require.Error(t, err, "%q must be refused", bad)
	}
}

// TestParseRepeat covers the bounded recurrence, and pins the unbounded
// rejection that SRD-077 §4.6 decided (T-5, FR-4, FR-7).
func TestParseRepeat(t *testing.T) {
	got, err := iso8601.ParseRepeat("R3/PT10H")
	require.NoError(t, err)
	require.Equal(t, 3, got.Count)
	require.Equal(t, 10*time.Hour, got.Interval)

	once, err := iso8601.ParseRepeat("R1/P1D")
	require.NoError(t, err)
	require.Equal(t, 1, once.Count)
	require.Equal(t, 24*time.Hour, once.Interval)

	for _, tc := range []struct {
		name, in, wantMsg string
	}{
		{"unbounded", "R/PT10H", "UNBOUNDED"},
		{"zero repetitions", "R0/PT10H", "at least once"},
		{"negative repetitions", "R-2/PT10H", "at least once"},
		{"missing R", "3/PT10H", "must start with R"},
		{"missing separator", "R3PT10H", "no / separating"},
		{"letters as the count", "RX/PT10H", "no valid repetition count"},
		{"bad interval", "R3/P1Y", "no fixed length"},
		{"zero interval", "R3/PT0S", "zero-length duration"},
		{"empty", "", "must start with R"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := iso8601.ParseRepeat(tc.in)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantMsg)
		})
	}
}
