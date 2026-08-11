// Package authtest publishes the AuthorizationProvider conformance suite
// (ADR-003 §4.2): every provider — the bundled allow-all default, or an
// adapter over a real IAM — proves the same decision contract by calling
// Conformance from a one-line test.
//
// AuthorizationProvider has one method, which shapes what a suite can say
// about it. ADR-003 §4.2 excuses the single-method sinks (Logger, Tracer,
// MetricsRecorder) from conformance suites for exactly that reason, yet lists
// authtest anyway — and the difference is real: those sinks SWALLOW a value
// and cannot answer wrongly, while Authorize RETURNS a decision the engine
// acts on. A sink that misbehaves loses a log line; a provider that
// misbehaves admits an unauthorized caller or locks out an authorized one.
//
// Two kinds of property follow. The universal ones — a provider answers every
// declared Action, tolerates a zero Request, and explains a denial — the suite
// checks unaided. WHICH requests should be allowed is policy, knowable only to
// the provider's author, so the caller supplies the verdicts it expects.
package authtest

import (
	"context"
	"strings"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/auth"
)

// Subject is the provider under test plus the verdicts it must reach.
type Subject struct {
	// Provider is the implementation under test.
	Provider auth.AuthorizationProvider

	// Allowed lists requests the provider must permit (Authorize returns nil).
	Allowed []auth.Request

	// Denied lists requests the provider must refuse (Authorize returns a
	// non-nil error).
	//
	// It may be empty, and that is not a gap in the test: allow-all is the
	// bundled default and denies nothing by design. A provider that DOES
	// enforce policy should list at least one denial here, or the suite can
	// only prove it says yes.
	Denied []auth.Request
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

// Factory builds a fresh Subject. It is called once per subtest.
type Factory func(t *testing.T) Subject

// Conformance runs the AuthorizationProvider contract against factory-built
// subjects. Adapter tests are one-liners:
//
//	func TestConformance(t *testing.T) {
//		authtest.Conformance(t, func(t *testing.T) authtest.Subject {
//			return authtest.Subject{
//				Provider: myiam.New(policy),
//				Allowed:  []auth.Request{{Subject: "alice", Action: auth.ActionStartProcess}},
//				Denied:   []auth.Request{{Subject: "mallory", Action: auth.ActionStartProcess}},
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
	"AnswersEveryDeclaredAction": testAnswersEveryDeclaredAction,
	"AnswersTheZeroRequest":      testAnswersTheZeroRequest,
	"AllowsWhatItMustAllow":      testAllowsWhatItMustAllow,
	"DeniesWhatItMustDeny":       testDeniesWhatItMustDeny,
	"DenialsExplainThemselves":   testDenialsExplainThemselves,
	"RepeatsItsVerdict":          testRepeatsItsVerdict,
}

// declaredActions are the sensitive operations the port names. A provider must
// have an answer for each: the engine calls Authorize before performing one,
// and a provider that panics on an Action it does not recognize takes the
// engine down rather than refusing the operation.
var declaredActions = []auth.Action{
	auth.ActionStartProcess,
	auth.ActionClaimUserTask,
	auth.ActionCancelInstance,
}

func testAnswersEveryDeclaredAction(t tb, s Subject) {
	ctx := context.Background()

	for _, a := range declaredActions {
		// The VERDICT is not asserted — that is policy. What is asserted is
		// that asking produces an answer rather than a panic.
		answers(t, "action "+string(a), func() error {
			return s.Provider.Authorize(ctx, auth.Request{
				Subject:  "conformance-subject",
				Resource: "conformance-resource",
				Action:   a,
			})
		})
	}
}

// answers runs ask and fails, naming what was asked, if it panics instead of
// returning. Recovering here rather than letting the panic escape is the
// difference between "the provider refuses action usertask.claim by panicking"
// and a bare stack trace out of the engine's authorization call.
func answers(t tb, what string, ask func() error) {
	t.Helper()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Authorize panicked on %s: %v — a provider must refuse, "+
				"not crash the engine that asked", what, r)
		}
	}()

	//nolint:errcheck // the decision is policy; answering at all is the test
	_ = ask()
}

// testAnswersTheZeroRequest: an empty request is malformed, and refusing it is
// a perfectly good answer — but it must be an ANSWER. A provider that indexes
// into an empty Subject and panics turns a bad call into a crash.
func testAnswersTheZeroRequest(t tb, s Subject) {
	answers(t, "the zero Request", func() error {
		return s.Provider.Authorize(context.Background(), auth.Request{})
	})
}

func testAllowsWhatItMustAllow(t tb, s Subject) {
	if len(s.Allowed) == 0 {
		t.Skip("Subject.Allowed is empty — nothing is claimed to be permitted")
	}

	ctx := context.Background()

	for _, r := range s.Allowed {
		if err := s.Provider.Authorize(ctx, r); err != nil {
			t.Fatalf("Authorize(%s on %q by %q) denied a request the provider "+
				"must allow: %v", r.Action, r.Resource, r.Subject, err)
		}
	}
}

func testDeniesWhatItMustDeny(t tb, s Subject) {
	if len(s.Denied) == 0 {
		t.Skip("Subject.Denied is empty — this provider denies nothing " +
			"(allow-all is a legitimate policy)")
	}

	ctx := context.Background()

	for _, r := range s.Denied {
		if err := s.Provider.Authorize(ctx, r); err == nil {
			t.Fatalf("Authorize(%s on %q by %q) allowed a request the provider "+
				"must deny", r.Action, r.Resource, r.Subject)
		}
	}
}

// testDenialsExplainThemselves: the interface says the error "describes the
// denial". An empty message reaches an operator as a bare refusal with no way
// to tell a policy decision from a broken IAM connection.
func testDenialsExplainThemselves(t tb, s Subject) {
	if len(s.Denied) == 0 {
		t.Skip("Subject.Denied is empty — no denial to inspect")
	}

	ctx := context.Background()

	for _, r := range s.Denied {
		err := s.Provider.Authorize(ctx, r)
		if err == nil {
			continue
		}

		if strings.TrimSpace(err.Error()) == "" {
			t.Fatalf("Authorize(%s) denied with an empty message — an operator "+
				"cannot tell a policy denial from a broken provider", r.Action)
		}
	}
}

// testRepeatsItsVerdict: asking twice about the same request gives the same
// answer. A provider whose verdict drifts between identical calls makes the
// engine's behavior unreproducible, and an authorization bug impossible to
// investigate from a log.
//
// It is checked only against the caller's own declared fixtures, so a provider
// that legitimately varies — rate-limited, time-windowed — simply declares no
// fixture whose verdict it cannot promise to repeat.
func testRepeatsItsVerdict(t tb, s Subject) {
	if len(s.Allowed) == 0 && len(s.Denied) == 0 {
		t.Skip("no declared verdicts to repeat")
	}

	ctx := context.Background()

	// Both calls happen HERE. Relying on an earlier subtest to have made the
	// first one would test nothing: Conformance builds a fresh Subject per
	// subtest, so this assertion's provider has never been asked before.
	for _, r := range s.Allowed {
		first := s.Provider.Authorize(ctx, r)

		if second := s.Provider.Authorize(ctx, r); (first == nil) !=
			(second == nil) {
			t.Fatalf("Authorize(%s) answered %v then %v on identical calls",
				r.Action, first, second)
		}
	}

	for _, r := range s.Denied {
		first := s.Provider.Authorize(ctx, r)

		if second := s.Provider.Authorize(ctx, r); (first == nil) !=
			(second == nil) {
			t.Fatalf("Authorize(%s) denied then allowed on a repeat: %v, %v",
				r.Action, first, second)
		}
	}
}
