package taskstest_test

import (
	"context"
	"os"
	"os/exec"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/clock/clocktest"
	"github.com/dr-dobermann/gobpm/pkg/tasks"
	"github.com/dr-dobermann/gobpm/pkg/tasks/localdispatcher"
	"github.com/dr-dobermann/gobpm/pkg/tasks/taskstest"
)

var base = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// TestLocalDispatcherConformance runs the published suite against the bundled
// dispatcher. It is what keeps the suite honest: a contract nothing executes
// drifts from the port it claims to describe.
func TestLocalDispatcherConformance(t *testing.T) {
	taskstest.Conformance(t, func(*testing.T) tasks.WorkerDispatcher {
		return localdispatcher.New(clocktest.New(base), time.Minute)
	})
}

// brokenDispatcher accepts every enqueue and hands out no work — a queue that
// silently swallows jobs, which is how a real dispatcher fails in production.
type brokenDispatcher struct{}

func (brokenDispatcher) Enqueue(context.Context, tasks.Job) error { return nil }

func (brokenDispatcher) FetchAndLock(
	context.Context, tasks.WorkerID, []tasks.Topic, time.Duration,
) ([]tasks.LockedJob, error) {
	return nil, nil
}

func (brokenDispatcher) ExtendLock(
	context.Context, tasks.JobID, tasks.WorkerID, time.Duration,
) error {
	return nil
}

func (brokenDispatcher) Complete(
	context.Context, tasks.JobID, tasks.WorkerID, *data.ItemDefinition,
) error {
	return nil
}

func (brokenDispatcher) ReportBpmnError(
	context.Context, tasks.JobID, tasks.WorkerID, string, string,
) error {
	return nil
}

func (brokenDispatcher) ReportStatus(
	context.Context, tasks.JobID, tasks.WorkerID, data.Value,
) error {
	return nil
}

func (brokenDispatcher) Fail(
	context.Context, tasks.JobID, tasks.WorkerID, tasks.Fault,
) error {
	return nil
}

// TestSuiteRejectsABrokenDispatcher is the suite's own negative control
// (SRD-090 T-9), run in a child process for the reason given in the
// messagingtest twin.
func TestSuiteRejectsABrokenDispatcher(t *testing.T) {
	if os.Getenv("GOBPM_CONFORMANCE_NEGATIVE") == "1" {
		taskstest.Conformance(t, func(*testing.T) tasks.WorkerDispatcher {
			return brokenDispatcher{}
		})

		return
	}

	cmd := exec.Command(os.Args[0],
		"-test.run=^TestSuiteRejectsABrokenDispatcher$/^EnqueueThenFetchLocks$",
		"-test.timeout=5m")
	cmd.Env = append(os.Environ(), "GOBPM_CONFORMANCE_NEGATIVE=1")

	if err := cmd.Run(); err == nil {
		t.Fatal("the conformance suite PASSED a dispatcher that hands out no " +
			"work — every enqueued service task would park forever")
	}
}
