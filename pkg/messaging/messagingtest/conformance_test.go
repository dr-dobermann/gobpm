package messagingtest_test

import (
	"context"
	"os"
	"os/exec"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/messaging"
	"github.com/dr-dobermann/gobpm/pkg/messaging/membroker"
	"github.com/dr-dobermann/gobpm/pkg/messaging/messagingtest"
)

// TestMembrokerConformance runs the published suite against the in-core
// default. It is what keeps the suite honest: a contract nothing executes
// drifts from the port it claims to describe.
func TestMembrokerConformance(t *testing.T) {
	messagingtest.Conformance(t, func(*testing.T) messaging.MessageBroker {
		return membroker.New()
	})
}

// brokenBroker accepts every publish and delivers nothing — the cheapest way
// to be wrong while still satisfying the interface.
type brokenBroker struct{}

func (brokenBroker) Publish(context.Context, messaging.Envelope) error {
	return nil
}

func (brokenBroker) Subscribe(
	context.Context, string, ...string,
) (messaging.Subscription, error) {
	return deadSubscription{}, nil
}

type deadSubscription struct{}

func (deadSubscription) C() <-chan messaging.Envelope { return nil }
func (deadSubscription) AddKey(string) error          { return nil }
func (deadSubscription) Unsubscribe() error           { return nil }

// TestSuiteRejectsABrokenBroker is the suite's own negative control (SRD-090
// T-9): a conformance helper that cannot fail proves nothing about the
// implementations it passes.
//
// It runs the suite against the broken broker in a CHILD PROCESS and requires
// a non-zero exit. Asserting "this fails" in-process is not possible: a failed
// subtest marks its parent failed, so the check would turn the package red
// whether or not the suite worked.
func TestSuiteRejectsABrokenBroker(t *testing.T) {
	if os.Getenv("GOBPM_CONFORMANCE_NEGATIVE") == "1" {
		messagingtest.Conformance(t, func(*testing.T) messaging.MessageBroker {
			return brokenBroker{}
		})

		return
	}

	// Only ONE subtest runs in the child. A suite that rejects the fake on any
	// single assertion has proved it can fail, and the broken broker never
	// delivers — so running the rest would only wait out one hang-breaker
	// after another, at ~5s each, on every gate run.
	cmd := exec.Command(os.Args[0],
		"-test.run=^TestSuiteRejectsABrokenBroker$/^SubscribeThenPublishDelivers$",
		"-test.timeout=5m")
	cmd.Env = append(os.Environ(), "GOBPM_CONFORMANCE_NEGATIVE=1")

	if err := cmd.Run(); err == nil {
		t.Fatal("the conformance suite PASSED a broker that delivers nothing " +
			"— the suite proves nothing about the brokers it accepts")
	}
}
