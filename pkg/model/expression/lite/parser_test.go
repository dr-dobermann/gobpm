package lite_test

import (
	"testing"
)

// TestGrammar covers SRD-067 T-1's happy half: literals, precedence and
// grouping over literal-only bodies (no data reads).
func TestGrammar(t *testing.T) {
	src := newSource(t, nil)

	cases := []struct {
		body string
		want any
	}{
		// literals
		{"150", 150.0},
		{"1.5", 1.5},
		{"'single'", "single"},
		{`"double"`, "double"},
		{`'it\'s'`, "it's"},
		{`'back\\slash'`, `back\slash`},
		{"true", true},
		{"false", false},

		// precedence: and binds tighter than or
		{"true or false and false", true},
		// not binds tighter than and, looser than comparison
		{"not 1 == 2", true},
		{"not not true", true},
		// arithmetic ladder
		{"2 + 3 * 4", 14.0},
		{"(2 + 3) * 4", 20.0},
		{"10 % 4", 2.0},
		{"-2 + 3", 1.0},
		{"- - 2", 2.0},
		{"10 / 4", 2.5},
		// comparison over arithmetic
		{"2 + 2 == 4", true},
		{"1 + 1 != 3", true},
		{"2 < 3", true},
		{"3 <= 3", true},
		{"4 > 5", false},
		{"5 >= 5", true},
		// strings compare and concatenate
		{"'a' < 'b'", true},
		{"'a' + 'b' == 'ab'", true},
		{"'b' > 'a'", true},
		{"'b' >= 'a'", true},
		{"'a' <= 'a'", true},
		{"'a' != 'b'", true},
		{"3 >= 3", true},
		{"3 <= 4", true},
	}

	for _, c := range cases {
		wantValue(t, src, c.body, c.want)
	}
}

// TestSyntaxErrors covers SRD-067 T-1's loud half: every malformed body
// fails with a classified error carrying its offset.
func TestSyntaxErrors(t *testing.T) {
	src := newSource(t, nil)

	cases := []struct {
		body   string
		marker string
	}{
		{"total >", "expected an expression"},
		{"1 +* 2", "expected an expression"},
		{"(1", "expected ')'"},
		{"foo(1)", "unknown function"},
		{"'abc", "unterminated string"},
		{"a == = b", "'=' must be '=='"},
		{"a ! b", "'!' must be '!='"},
		{"5.", "a digit must follow the decimal point"},
		{"1 2", "unexpected input after the expression"},
		{"a < b < c", "unexpected input after the expression"},
		{"@", "unexpected character"},
		{`'a\x'`, `only the quote and '\' may be escaped`},
		{"items[", "unterminated '[' path segment"},
		{"items[x]", "a path segment needs an index or a quoted key"},
		{"items[0", "must close with ']'"},
		{`rates["EUR`, "unterminated key in a path segment"},
		{"len(1", "expected ')' after the argument"},
		{"not (1", "expected ')'"},
		{"-(1", "expected ')'"},
		{"1 + (", "expected an expression"},
	}

	for _, c := range cases {
		wantError(t, src, c.body, c.marker, "offset")
	}
}
