package goexpr

import (
	"testing"

	dgexpr "github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
)

// TestWidenedContract covers SRD-066 T-2: the kind, the claim, and the
// unchanged delegation (the existing delegate test keeps covering
// Evaluate).
func TestWidenedContract(t *testing.T) {
	e := New()

	if e.Type() != GoExprType || e.Type() != "##GoExpr" {
		t.Fatalf("Type() = %q, want ##GoExpr", e.Type())
	}

	ll := e.Languages()
	if len(ll) != 1 || ll[0] != dgexpr.Language {
		t.Fatalf("Languages() = %v, want [%s]", ll, dgexpr.Language)
	}
}
