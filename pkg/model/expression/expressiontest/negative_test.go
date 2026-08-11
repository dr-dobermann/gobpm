package expressiontest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
)

// fakeTB records what an assertion did instead of failing the real test. Fatal
// and Skip abort by panicking, the way *testing.T aborts by Goexit.
type fakeTB struct {
	msg     string
	failed  bool
	skipped bool
}

// fakeAbort is the sentinel a fakeTB panics with, so drive can tell an
// intentional abort from a genuine panic and re-raise the latter.
type fakeAbort struct{}

func (f *fakeTB) Helper()        {}
func (f *fakeTB) Cleanup(func()) {}

func (f *fakeTB) Fatal(args ...any) {
	f.failed, f.msg = true, fmt.Sprint(args...)

	panic(fakeAbort{})
}

func (f *fakeTB) Fatalf(format string, args ...any) {
	f.failed, f.msg = true, fmt.Sprintf(format, args...)

	panic(fakeAbort{})
}

func (f *fakeTB) Skip(args ...any) {
	f.skipped, f.msg = true, fmt.Sprint(args...)

	panic(fakeAbort{})
}

// drive runs one contract assertion against s and reports what it did.
func drive(test func(tb, Subject), s Subject) *fakeTB {
	f := &fakeTB{}

	func() {
		defer func() {
			if r := recover(); r != nil {
				if _, ok := r.(fakeAbort); !ok {
					panic(r)
				}
			}
		}()

		test(f, s)
	}()

	return f
}

// stubEngine is a configurable engine: each field is one way to violate the
// contract, so a test names the violation it is checking.
type stubEngine struct {
	kind      string
	langs     []string
	drift     bool
	result    data.Value
	evalErr   error
	acceptNil bool
	asked     int
}

func (e *stubEngine) Type() string { return e.kind }

func (e *stubEngine) Languages() []string {
	e.asked++

	if e.drift && e.asked > 1 {
		return []string{"changed"}
	}

	return e.langs
}

func (e *stubEngine) Evaluate(
	_ context.Context, expr data.FormalExpression, src data.Source,
) (data.Value, error) {
	if expr == nil && !e.acceptNil {
		return nil, errors.New("nil expression")
	}

	if src == nil && !e.acceptNil {
		return nil, errors.New("nil source")
	}

	return e.result, e.evalErr
}

// good is an engine that satisfies every assertion.
func good() *stubEngine {
	return &stubEngine{
		kind:   "##Stub",
		langs:  []string{"stub:lang"},
		result: values.NewVariable(2),
	}
}

// stubExpr is a placeholder FormalExpression: the suite only passes it through.
type stubExpr struct{ data.FormalExpression }

// stubSource is a placeholder data.Source, for the same reason.
type stubSource struct{}

func (stubSource) Find(context.Context, string) (data.Data, error) {
	return nil, errors.New("no data")
}

func subject(e *stubEngine) Subject {
	return Subject{
		Engine:    e,
		Expr:      stubExpr{},
		Source:    stubSource{},
		Want:      2,
		CheckWant: true,
	}
}

// TestAssertionsRejectBrokenEngines drives each contract assertion against an
// engine that violates precisely it (SRD-088 T-9, in-process half).
func TestAssertionsRejectBrokenEngines(t *testing.T) {
	nameless := good()
	nameless.kind = "  "

	mute := good()
	mute.langs = nil

	blank := good()
	blank.langs = []string{""}

	drifting := good()
	drifting.drift = true

	failing := good()
	failing.evalErr = errors.New("boom")

	silent := good()
	silent.result = nil

	wrong := good()
	wrong.result = values.NewVariable(99)

	lax := good()
	lax.acceptNil = true

	for name, tc := range map[string]struct {
		test func(tb, Subject)
		subj Subject
		want string
	}{
		"an engine with no kind": {
			test: testTypeIsNamed, subj: subject(nameless), want: "is empty",
		},
		"an engine claiming no language": {
			test: testLanguagesAreClaimed, subj: subject(mute), want: "is empty",
		},
		"an engine claiming a blank language": {
			test: testLanguagesAreClaimed, subj: subject(blank), want: "blank",
		},
		"an engine whose claim drifts": {
			test: testLanguagesAreStable, subj: subject(drifting),
			want: "changed between calls",
		},
		"an engine that cannot evaluate its own subject": {
			test: testEvaluatesItsOwnSubject, subj: subject(failing),
			want: "own subject failed",
		},
		"an engine returning nil with no error": {
			test: testEvaluatesItsOwnSubject, subj: subject(silent),
			want: "nil Value and a nil error",
		},
		"an engine producing the wrong value": {
			test: testEvaluatesItsOwnSubject, subj: subject(wrong),
			want: "want 2",
		},
		"an engine accepting a nil expression": {
			test: testNilExpressionRejected, subj: subject(lax),
			want: "must be rejected",
		},
		"an engine accepting a nil source it declared required": {
			test: testNilSourceRejected,
			subj: func() Subject {
				s := subject(lax)
				s.SourceRequired = true

				return s
			}(),
			want: "must be rejected",
		},
		"a Subject with no expression": {
			test: testEvaluatesItsOwnSubject,
			subj: Subject{Engine: good()},
			want: "are required",
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := drive(tc.test, tc.subj)

			if !got.failed {
				t.Fatalf("the assertion PASSED %s", name)
			}

			if !strings.Contains(got.msg, tc.want) {
				t.Fatalf("failed with %q, which does not mention %q",
					got.msg, tc.want)
			}
		})
	}
}

// TestAssertionsPassAConformingEngine is the other direction: an assertion
// that always failed would satisfy the test above and be equally useless.
func TestAssertionsPassAConformingEngine(t *testing.T) {
	for name, test := range conformanceTests {
		t.Run(name, func(t *testing.T) {
			got := drive(test, subject(good()))

			if got.failed {
				t.Fatalf("a conforming engine was rejected: %s", got.msg)
			}
		})
	}
}

// TestNilSourceSkipsWhenNotRequired: an engine that may evaluate without a
// source must not be asked to reject one.
func TestNilSourceSkipsWhenNotRequired(t *testing.T) {
	got := drive(testNilSourceRejected, subject(good()))

	if !got.skipped {
		t.Fatal("SourceRequired=false must SKIP — asserting rejection would " +
			"fail a self-sourced engine like goexpr")
	}
}
