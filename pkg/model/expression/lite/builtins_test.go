package lite_test

import (
	"testing"
)

// TestBuiltins covers SRD-067 T-4: has (probe semantics), len (elements,
// keys, runes) and time (RFC3339).
func TestBuiltins(t *testing.T) {
	src := adrSource(t)

	t.Run("has probes without faulting",
		func(t *testing.T) {
			wantValue(t, src, "has('total')", true)
			wantValue(t, src, "has('ghost')", false)
			wantValue(t, src, "has('order.ghost')", false)
			wantValue(t, src, `has('rates["EUR"]')`, true)
			wantValue(t, src, `has('rates["GBP"]')`, false)
			wantError(t, src, "has(ghost)", "reading")
			wantError(t, src, "has(5)",
				"has() needs a string naming a datum or path")
		})

	t.Run("len counts elements, keys and runes",
		func(t *testing.T) {
			wantValue(t, src, "len(items)", 2.0)
			wantValue(t, src, "len(rates)", 2.0)
			wantValue(t, src, "len(tier)", 4.0)
			wantValue(t, src, "len('привет')", 6.0)
			wantError(t, src, "len('a' + 1)", "different kinds")
			wantError(t, src, "len(5)",
				"len() needs an array, a map or a string")
			wantError(t, src, "len(total)",
				"len() needs an array, a map or a string")
			wantError(t, src, "len(ghost)", "reading \"ghost\" failed")
		})

	t.Run("time parses RFC3339 literals",
		func(t *testing.T) {
			wantValue(t, src,
				`time("2026-08-01T00:00:00Z") < deadline`, true)
			wantValue(t, src, "updated_at >= created_at", true)
			wantValue(t, src, "created_at <= updated_at", true)
			wantValue(t, src, "deadline != created_at", true)
			wantValue(t, src,
				`time("2026-08-01T00:00:00Z") ==`+
					` time("2026-08-01T02:00:00+02:00")`, true)
			wantError(t, src, "time('nope')",
				"isn't an RFC3339 timestamp")
			wantError(t, src, "time(ghost)", "reading")
			wantError(t, src, "time(5)",
				"time() needs an RFC3339 string")
		})
}
