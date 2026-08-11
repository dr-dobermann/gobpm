// Package expressiontest publishes the ExpressionEngine conformance suite
// (ADR-003 §4.2): every engine — the Go-native default, lite, or a FEEL/JUEL
// adapter — proves the same contract by calling Conformance from a one-line
// test.
//
// The suite is shaped by one fact about this port that the others do not
// share: an expression's BODY is written in the engine's own language, so the
// suite cannot author a working expression itself. A FEEL engine and a
// Go-native engine agree on nothing about what an expression looks like. The
// caller therefore supplies one working Subject — an expression, a source, and
// the value it must produce — and the suite checks the parts that ARE common:
// the identity claims the Registry routes on, the parameter guards, and that
// the supplied expression round-trips.
package expressiontest

import (
	"context"
	"strings"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/expression"
)

// Subject is the engine under test plus one expression it can evaluate.
type Subject struct {
	// Engine is the implementation under test.
	Engine expression.Engine

	// Expr is an expression written in Engine's own language, which must
	// evaluate against Source without error.
	Expr data.FormalExpression

	// Source is the data source Expr reads.
	Source data.Source

	// Want is the value Expr must produce, compared against the evaluated
	// result's Get(ctx). It is only consulted when CheckWant is set.
	//
	// The comparison is by Go equality, so the type matters: lite evaluates
	// arithmetic in float64 where a Go-native functor returns int, and both
	// are correct. State the value the engine actually produces.
	Want any

	// CheckWant asserts the evaluated result equals Want. Without it the suite
	// checks only that evaluation succeeds and returns a non-nil Value.
	//
	// It exists because "Want is nil" and "do not check the value" are
	// different claims, and conflating them made the interesting case
	// unsayable: an engine whose expression correctly evaluates to nil — a
	// missing field, an empty lookup — could not assert that, because the
	// suite read the nil as "skip" and silently checked nothing.
	CheckWant bool

	// SourceRequired declares that this engine cannot evaluate without a
	// caller-supplied data.Source, making Evaluate(ctx, expr, nil) an error.
	//
	// It is opt-in because the port genuinely differs here, and discovering
	// that is what put the field in: lite reads its operands from the source
	// and must reject a nil one, while a goexpr functor may carry a source
	// bound at construction and legitimately evaluates with nil. Asserting
	// either behavior for every engine would fail a correct implementation
	// of the other kind.
	SourceRequired bool
}

// tb is the slice of *testing.T the individual contract assertions use. It
// exists so the suite's OWN failure branches can be driven in-process by a
// recording fake: those branches only run against a broken implementation, and
// an assertion that is never executed is an assertion nobody has checked —
// an inverted comparison would silently pass every adapter it was meant to
// reject.
//
// Conformance still takes a real *testing.T, because subtests need one.
type tb interface {
	Helper()
	Cleanup(func())
	Fatal(args ...any)
	Fatalf(format string, args ...any)
	Skip(args ...any)
}

// Factory builds a fresh Subject. It is called once per subtest, so an engine
// carrying state must return an isolated one.
type Factory func(t *testing.T) Subject

// Conformance runs the full ExpressionEngine contract against factory-built
// subjects. Adapter tests are one-liners:
//
//	func TestConformance(t *testing.T) {
//		expressiontest.Conformance(t, func(t *testing.T) expressiontest.Subject {
//			return expressiontest.Subject{
//				Engine: lite.New(),
//				Expr:   myExpr(t, "a + 1"),
//				Source: mySource(t, "a", 1),
//				Want:   2,
//			}
//		})
//	}
func Conformance(t *testing.T, factory Factory) {
	t.Helper()

	if factory == nil {
		t.Fatal("Conformance: a nil Factory isn't allowed")
	}

	for name, test := range conformanceTests {
		t.Run(name, func(t *testing.T) { test(t, factory(t)) })
	}
}

// conformanceTests is the contract as a declarative table.
var conformanceTests = map[string]func(tb, Subject){
	"TypeIsNamed":            testTypeIsNamed,
	"LanguagesAreClaimed":    testLanguagesAreClaimed,
	"LanguagesAreStable":     testLanguagesAreStable,
	"EvaluatesItsOwnSubject": testEvaluatesItsOwnSubject,
	"NilExpressionRejected":  testNilExpressionRejected,
	"NilSourceRejected":      testNilSourceRejected,
}

// testTypeIsNamed: Type is the kind the startup config and the routing table
// print. An engine with no name cannot be reported or diagnosed.
func testTypeIsNamed(t tb, s Subject) {
	if strings.TrimSpace(s.Engine.Type()) == "" {
		t.Fatal("Type() is empty — the engine kind names the engine in the " +
			"startup config and the routing table")
	}
}

// testLanguagesAreClaimed: the interface calls the claim "never empty for a
// real engine". The Registry routes on it, so an engine claiming nothing is
// unreachable — wired, reported, and never called.
func testLanguagesAreClaimed(t tb, s Subject) {
	langs := s.Engine.Languages()
	if len(langs) == 0 {
		t.Fatal("Languages() is empty — the Registry routes on this claim, " +
			"so the engine would never be reached")
	}

	for i, l := range langs {
		if strings.TrimSpace(l) == "" {
			t.Fatalf("Languages()[%d] is blank — a blank URI matches nothing "+
				"and collides with every other engine's blank claim", i)
		}
	}
}

// testLanguagesAreStable: two calls must agree. The Registry reads the claim
// once at construction and routes on it forever, so an engine whose answer
// changes is routed by a table that no longer describes it.
func testLanguagesAreStable(t tb, s Subject) {
	first, second := s.Engine.Languages(), s.Engine.Languages()

	if len(first) != len(second) {
		t.Fatalf("Languages() returned %d claims then %d — the Registry reads "+
			"it once and routes on it forever", len(first), len(second))
	}

	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("Languages()[%d] changed between calls: %q then %q",
				i, first[i], second[i])
		}
	}
}

// testEvaluatesItsOwnSubject: the caller's expression round-trips. This is the
// only positive path the suite can check without knowing the language.
func testEvaluatesItsOwnSubject(t tb, s Subject) {
	if s.Expr == nil || s.Source == nil {
		t.Fatal("Subject.Expr and Subject.Source are required — without them " +
			"the suite cannot check that the engine evaluates anything at all")
	}

	ctx := context.Background()

	got, err := s.Engine.Evaluate(ctx, s.Expr, s.Source)
	if err != nil {
		t.Fatalf("Evaluate on the engine's own subject failed: %v", err)
	}

	if got == nil {
		t.Fatal("Evaluate returned a nil Value and a nil error — a caller " +
			"cannot tell success from failure")
	}

	if !s.CheckWant {
		return
	}

	if v := got.Get(ctx); v != s.Want {
		t.Fatalf("Evaluate produced %v (%T), want %v (%T)",
			v, v, s.Want, s.Want)
	}
}

// testNilExpressionRejected: a nil expression is the caller's bug, and the
// engine is the public boundary that must name it rather than panic deep
// inside evaluation.
func testNilExpressionRejected(t tb, s Subject) {
	if _, err := s.Engine.Evaluate(
		context.Background(), nil, s.Source,
	); err == nil {
		t.Fatal("Evaluate(nil expression) must be rejected")
	}
}

// testNilSourceRejected runs only for an engine that declared it needs a
// source. See Subject.SourceRequired for why this is not universal.
func testNilSourceRejected(t tb, s Subject) {
	if !s.SourceRequired {
		t.Skip("SourceRequired is false — this engine may evaluate without " +
			"a caller-supplied source")
	}

	if _, err := s.Engine.Evaluate(
		context.Background(), s.Expr, nil,
	); err == nil {
		t.Fatal("Evaluate(nil source) must be rejected by an engine that " +
			"declares SourceRequired")
	}
}
