package authtest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/auth"
)

// fakeTB records what an assertion did instead of failing the real test. Fatal
// and Skip abort by panicking, the way *testing.T aborts by Goexit, so an
// assertion that bails halfway does not run on into code it just guarded.
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

// permissive allows everything, including what a Subject declares denied.
type permissive struct{}

func (permissive) Authorize(context.Context, auth.Request) error { return nil }

// blocker refuses one subject and permits the rest.
type blocker struct{ blocked string }

func (b blocker) Authorize(_ context.Context, req auth.Request) error {
	if req.Subject == b.blocked {
		return errors.New("denied by policy")
	}

	return nil
}

// panicky crashes rather than deciding — the failure an engine must survive.
type panicky struct{}

func (panicky) Authorize(context.Context, auth.Request) error {
	panic("no policy loaded")
}

// mute denies, but says nothing about why.
type mute struct{}

func (mute) Authorize(context.Context, auth.Request) error {
	return errors.New("")
}

// flipper denies once, then allows — the drift testRepeatsItsVerdict catches.
type flipper struct{ asked int }

func (f *flipper) Authorize(context.Context, auth.Request) error {
	f.asked++
	if f.asked == 1 {
		return errors.New("denied")
	}

	return nil
}

var (
	deniedReqs = []auth.Request{
		{Subject: "mallory", Resource: "p", Action: auth.ActionStartProcess},
	}
	allowedReqs = []auth.Request{
		{Subject: "alice", Resource: "p", Action: auth.ActionStartProcess},
	}
)

// TestAssertionsRejectBrokenProviders drives each contract assertion against a
// provider that violates precisely it, and requires the assertion to fail.
//
// This is the in-process half of SRD-090 T-9. The child-process control proves
// Conformance as a whole rejects a broken provider; this proves each assertion
// does its OWN job, which a suite-level check cannot show — one assertion
// carrying all the weight while the others were inverted would look identical
// from outside.
func TestAssertionsRejectBrokenProviders(t *testing.T) {
	for name, tc := range map[string]struct {
		test func(tb, Subject)
		subj Subject
		want string
	}{
		"a provider that allows what must be denied": {
			test: testDeniesWhatItMustDeny,
			subj: Subject{Provider: permissive{}, Denied: deniedReqs},
			want: "must deny",
		},
		"a provider that denies what must be allowed": {
			test: testAllowsWhatItMustAllow,
			subj: Subject{
				Provider: blocker{blocked: "alice"},
				Allowed:  allowedReqs,
			},
			want: "must allow",
		},
		"a denial with an empty message": {
			test: testDenialsExplainThemselves,
			subj: Subject{Provider: mute{}, Denied: deniedReqs},
			want: "empty message",
		},
		"a provider that panics instead of deciding": {
			test: testAnswersEveryDeclaredAction,
			subj: Subject{Provider: panicky{}},
			want: "panicked",
		},
		"a provider that panics on the zero request": {
			test: testAnswersTheZeroRequest,
			subj: Subject{Provider: panicky{}},
			want: "panicked",
		},
		"a verdict that flips between identical calls": {
			test: testRepeatsItsVerdict,
			subj: Subject{Provider: &flipper{}, Denied: deniedReqs},
			want: "denied then allowed",
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := drive(tc.test, tc.subj)

			if !got.failed {
				t.Fatalf("the assertion PASSED %s — it cannot reject what it "+
					"exists to reject", name)
			}

			if !strings.Contains(got.msg, tc.want) {
				t.Fatalf("failed with %q, which does not mention %q — the "+
					"message is what an adapter author has to act on",
					got.msg, tc.want)
			}
		})
	}
}

// TestAssertionsPassAConformingProvider is the other direction: the same
// assertions must NOT fire on a provider that behaves. An assertion that
// always fails would satisfy the test above and be equally useless.
func TestAssertionsPassAConformingProvider(t *testing.T) {
	subj := Subject{
		Provider: blocker{blocked: "mallory"},
		Allowed:  allowedReqs,
		Denied:   deniedReqs,
	}

	for name, test := range conformanceTests {
		t.Run(name, func(t *testing.T) {
			got := drive(test, subj)

			if got.failed {
				t.Fatalf("a conforming provider was rejected: %s", got.msg)
			}
		})
	}
}

// TestSkipsWhenNothingIsDeclared: a Subject with no verdicts skips rather than
// passing vacuously, so an adapter author cannot mistake "nothing was checked"
// for "the provider conformed".
func TestSkipsWhenNothingIsDeclared(t *testing.T) {
	for name, test := range map[string]func(tb, Subject){
		"AllowsWhatItMustAllow":    testAllowsWhatItMustAllow,
		"DeniesWhatItMustDeny":     testDeniesWhatItMustDeny,
		"DenialsExplainThemselves": testDenialsExplainThemselves,
		"RepeatsItsVerdict":        testRepeatsItsVerdict,
	} {
		t.Run(name, func(t *testing.T) {
			got := drive(test, Subject{Provider: permissive{}})

			if !got.skipped {
				t.Fatal("an empty Subject must SKIP, not pass — a vacuous " +
					"pass reads as a conformant provider")
			}
		})
	}
}
