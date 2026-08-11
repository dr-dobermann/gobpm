package goexpr

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// fakeExpr is a minimal data.FormalExpression that records the Evaluate call.
type fakeExpr struct {
	gotSrc data.Source
	err    error
	called bool
}

func (f *fakeExpr) ID() string                        { return "fake" }
func (f *fakeExpr) Docs() []*foundation.Documentation { return nil }
func (f *fakeExpr) Language() string                  { return "test" }
func (f *fakeExpr) Result() (data.Value, error)       { return nil, nil }
func (f *fakeExpr) ResultType() string                { return "" }
func (f *fakeExpr) IsEvaluated() bool                 { return false }

func (f *fakeExpr) Evaluate(_ context.Context, src data.Source) (data.Value, error) {
	f.called = true
	f.gotSrc = src

	return nil, f.err
}

func TestEngineDelegatesToExpression(t *testing.T) {
	sentinel := errors.New("boom")
	expr := &fakeExpr{err: sentinel}

	eng := New()
	_, err := eng.Evaluate(context.Background(), expr, nil)

	if !expr.called {
		t.Fatal("Engine.Evaluate did not call the expression's Evaluate")
	}

	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want the expression's error", err)
	}
}

// TestEngineRejectsANilExpression: the engine is a public extension point, so
// a nil expression is named here rather than dereferenced.
//
// The guard exists because it did not: Evaluate delegated straight into the
// nil and panicked, which reports the library as broken instead of the call.
// expressiontest's NilExpressionRejected covers it too, but from another
// package — this package's own profile is what the coverage gate reads.
func TestEngineRejectsANilExpression(t *testing.T) {
	_, err := New().Evaluate(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("Evaluate(nil expression) must be rejected, not dereferenced")
	}

	if !strings.Contains(err.Error(), "nil FormalExpression") {
		t.Fatalf("err = %v, want it to name the nil FormalExpression", err)
	}
}

// TestEngineAcceptsANilSource pins the asymmetry with lite.Engine: a
// GExpression may carry a source bound at construction, and substituteSource
// uses it precisely when the passed one is nil, so rejecting nil here would
// break self-sourced functors.
func TestEngineAcceptsANilSource(t *testing.T) {
	expr := &fakeExpr{}

	if _, err := New().Evaluate(context.Background(), expr, nil); err != nil {
		t.Fatalf("Evaluate with a nil source must reach the expression: %v", err)
	}

	if !expr.called {
		t.Fatal("a nil source must be passed through, not rejected")
	}
}
