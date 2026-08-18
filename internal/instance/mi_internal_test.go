package instance

import (
	"math"
	"testing"
)

// TestCardinalityCount pins the count reader's whole boundary: the two
// engine-real shapes (a goexpr int, a lite integral float64), the
// fractional refusal, and the bounds guard — ±Inf and any float64
// beyond the int range pass the Trunc identity, and an unguarded cast
// there is implementation-dependent (the pre-merge review's finding).
func TestCardinalityCount(t *testing.T) {
	tests := map[string]struct {
		in     any
		want   int
		wantOK bool
	}{
		"int":                 {in: 3, want: 3, wantOK: true},
		"integral float64":    {in: 3.0, want: 3, wantOK: true},
		"fractional float64":  {in: 2.5, wantOK: false},
		"out-of-range 1e100":  {in: 1e100, wantOK: false},
		"positive infinity":   {in: math.Inf(1), wantOK: false},
		"negative infinity":   {in: math.Inf(-1), wantOK: false},
		"not a number at all": {in: "3", wantOK: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got, ok := cardinalityCount(tc.in)
			if ok != tc.wantOK || got != tc.want {
				t.Fatalf("cardinalityCount(%v) = (%d, %t), want (%d, %t)",
					tc.in, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}
