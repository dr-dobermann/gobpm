package goexpr

import (
	"context"
	"errors"
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

func (f *fakeExpr) ID() string                          { return "fake" }
func (f *fakeExpr) Docs() []*foundation.Documentation   { return nil }
func (f *fakeExpr) Language() string                    { return "test" }
func (f *fakeExpr) Result() (data.Value, error)         { return nil, nil }
func (f *fakeExpr) ResultType() string                  { return "" }
func (f *fakeExpr) IsEvaluated() bool                   { return false }

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
