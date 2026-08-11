package authtest_test

import (
	"context"
	"os"
	"os/exec"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/auth"
	"github.com/dr-dobermann/gobpm/pkg/auth/allowall"
	"github.com/dr-dobermann/gobpm/pkg/auth/authtest"
	"github.com/dr-dobermann/gobpm/pkg/errs"
)

// TestAllowAllConformance runs the published suite against the bundled
// default. Its Denied set is empty because allow-all denies nothing — which
// leaves the suite's denial subtests skipped, and is why the next test exists.
func TestAllowAllConformance(t *testing.T) {
	authtest.Conformance(t, func(*testing.T) authtest.Subject {
		return authtest.Subject{
			Provider: allowall.New(),
			Allowed: []auth.Request{
				{
					Subject:  "alice",
					Resource: "order-process",
					Action:   auth.ActionStartProcess,
				},
				{
					Subject:  "bob",
					Resource: "task-7",
					Action:   auth.ActionClaimUserTask,
				},
			},
		}
	})
}

// denyProvider refuses one subject and permits the rest — the smallest
// provider that actually decides.
type denyProvider struct{ blocked string }

func (p denyProvider) Authorize(_ context.Context, req auth.Request) error {
	if req.Subject == p.blocked {
		return errs.New(
			errs.M("subject %q may not %s %q", req.Subject, req.Action,
				req.Resource),
			errs.C("AUTHTEST", errs.InvalidParameter))
	}

	return nil
}

// TestDenyingProviderConformance exercises the half of the suite that
// allow-all cannot reach.
//
// Without it the denial subtests would skip on the only provider in the tree,
// and a suite whose deny branch never executes is untested code that adapter
// authors would nonetheless rely on. It also pins the asymmetry the suite is
// built around: allowing and denying are both conformant, so the suite checks
// the verdicts the caller declares rather than inventing a policy.
func TestDenyingProviderConformance(t *testing.T) {
	authtest.Conformance(t, func(*testing.T) authtest.Subject {
		return authtest.Subject{
			Provider: denyProvider{blocked: "mallory"},
			Allowed: []auth.Request{
				{
					Subject:  "alice",
					Resource: "order-process",
					Action:   auth.ActionStartProcess,
				},
			},
			Denied: []auth.Request{
				{
					Subject:  "mallory",
					Resource: "order-process",
					Action:   auth.ActionStartProcess,
				},
				{
					Subject:  "mallory",
					Resource: "instance-3",
					Action:   auth.ActionCancelInstance,
				},
			},
		}
	})
}

// permissiveProvider allows everything, including the requests its Subject
// declares as denied — the failure mode that matters for an authorization
// port, since it admits callers rather than merely inconveniencing them.
type permissiveProvider struct{}

func (permissiveProvider) Authorize(context.Context, auth.Request) error {
	return nil
}

// TestSuiteRejectsAPermissiveProvider is the suite's own negative control
// (SRD-090 T-9), run in a child process for the reason given in the
// messagingtest twin.
func TestSuiteRejectsAPermissiveProvider(t *testing.T) {
	if os.Getenv("GOBPM_CONFORMANCE_NEGATIVE") == "1" {
		authtest.Conformance(t, func(*testing.T) authtest.Subject {
			return authtest.Subject{
				Provider: permissiveProvider{},
				Denied: []auth.Request{
					{
						Subject:  "mallory",
						Resource: "order-process",
						Action:   auth.ActionStartProcess,
					},
				},
			}
		})

		return
	}

	cmd := exec.Command(os.Args[0],
		"-test.run=^TestSuiteRejectsAPermissiveProvider$/^DeniesWhatItMustDeny$",
		"-test.timeout=5m")
	cmd.Env = append(os.Environ(), "GOBPM_CONFORMANCE_NEGATIVE=1")

	if err := cmd.Run(); err == nil {
		t.Fatal("the conformance suite PASSED a provider that allows a request " +
			"its own Subject declares denied")
	}
}
