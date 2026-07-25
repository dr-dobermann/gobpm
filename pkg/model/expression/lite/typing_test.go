package lite_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
)

// TestTypeDiscipline covers SRD-067 T-3: cross-kind comparisons are
// loud, boolean operators demand bools, division by zero is loud,
// short-circuiting is real, nil equates only, and a whole-expression
// nil result is loud.
func TestTypeDiscipline(t *testing.T) {
	src := adrSource(t)

	t.Run("cross-kind comparisons are loud",
		func(t *testing.T) {
			wantError(t, src, "'a' > 1", "different kinds")
			wantError(t, src, "tier > total", "different kinds")
			wantError(t, src, "deadline > 5", "different kinds")
			wantError(t, src, "approved == 1", "different kinds")
			wantError(t, src, "'a' + 1", "different kinds")
		})

	t.Run("ordering is defined only where it means something",
		func(t *testing.T) {
			wantError(t, src, "true > false", "bools don't order")
			wantError(t, src, "nil < 5", "nil doesn't order")
		})

	t.Run("boolean operators demand bools",
		func(t *testing.T) {
			wantError(t, src, "not 5", "'not' needs a bool")
			wantError(t, src, "true and 1", "'and' needs bool")
			wantError(t, src, "1 or true", "'or' needs bool")
			wantError(t, src, "ghost == 1 and true", "reading")
			wantError(t, src, "true and ghost == 1", "reading")
		})

	t.Run("arithmetic guards",
		func(t *testing.T) {
			wantError(t, src, "5 / 0", "division by zero")
			wantError(t, src, "5 % 0", "division by zero")
			wantError(t, src, "-'a'", "unary '-' needs a number")
			wantError(t, src, "'a' - 'b'", "needs number operands")
			wantError(t, src, "1 + true", "needs number operands")
		})

	t.Run("short-circuit proven: the right side would fault",
		func(t *testing.T) {
			wantValue(t, src, "false and ghost > 1", false)
			wantValue(t, src, "true or ghost > 1", true)
		})

	t.Run("nil equates only",
		func(t *testing.T) {
			wantValue(t, src, "nil == nil", true)
			wantValue(t, src, "nil != nil", false)
			wantValue(t, src, "nil == 5", false)
			wantValue(t, src, "5 != nil", true)
		})

	t.Run("a whole-expression nil result is loud",
		func(t *testing.T) {
			wantError(t, src, "nil", "produced no value")
		})

	t.Run("the whole numeric family unifies to float64",
		func(t *testing.T) {
			nums := newSource(t, map[string]data.Value{
				"i":   values.NewVariable(int(1)),
				"i8":  values.NewVariable(int8(2)),
				"i16": values.NewVariable(int16(3)),
				"i32": values.NewVariable(int32(4)),
				"i64": values.NewVariable(int64(5)),
				"u":   values.NewVariable(uint(6)),
				"u8":  values.NewVariable(uint8(7)),
				"u16": values.NewVariable(uint16(8)),
				"u32": values.NewVariable(uint32(9)),
				"u64": values.NewVariable(uint64(10)),
				"f32": values.NewVariable(float32(11)),
			})

			wantValue(t, nums,
				"i + i8 + i16 + i32 + i64 + u + u8 + u16 + u32"+
					" + u64 + f32",
				66.0)
		})

	t.Run("data reads fail loud",
		func(t *testing.T) {
			wantError(t, src, "ghost > 1", "reading \"ghost\" failed")
			wantError(t, src, "order.ghost > 1",
				"reading \"order.ghost\" failed")
			// a bare array ref reads the cursor's CURRENT element
			// (the collection contract) — here a string, cross-kind:
			wantError(t, src, "items > 1", "different kinds")
			// maps and records snapshot to Go maps — not operands:
			wantError(t, src, "rates > 1", "isn't a lite operand")
			wantError(t, src, "order == order", "isn't a lite operand")
		})
}
