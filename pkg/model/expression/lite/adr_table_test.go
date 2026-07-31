package lite_test

import (
	"testing"
)

// TestADRWorkedExamples covers SRD-067 T-2: the ten ADR-032 §2.3
// worked-example rows verbatim, over real values kinds (Variables of
// every payload, an Array, nested Records and a Map).
func TestADRWorkedExamples(t *testing.T) {
	src := adrSource(t)

	cases := []struct {
		body string
		want any
	}{
		{"total > 100 and total <= 500", true},
		{"total * (1 + rate)", 178.5},
		{`tier == "gold" or tier == "vip"`, true},
		{`"Dear " + name`, "Dear Ann"},
		{"approved and not blocked", true},
		{`deadline > time("2026-08-01T00:00:00Z")`, true},
		{"updated_at > created_at", true},
		{`len(items) > 0 and items[0] == "sku-1"`, true},
		{`order.total > 100 and order.customer.tier == "vip"`, true},
		{`has('rates["EUR"]') and rates["EUR"] < 1.2`, true},
	}

	for _, c := range cases {
		wantValue(t, src, c.body, c.want)
	}
}

// TestBoolComparison covers compare's bool arm (FIX-034 split it into
// compareBools so both operands are checked): equality and inequality over two
// boolean variables, which no ADR row exercises.
func TestBoolComparison(t *testing.T) {
	src := adrSource(t)

	wantValue(t, src, "approved == blocked", false)
	wantValue(t, src, "approved != blocked", true)
	wantValue(t, src, "approved == approved", true)
}
