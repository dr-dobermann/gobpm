package localdispatcher_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/clock/clocktest"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/tasks/localdispatcher"
	"github.com/stretchr/testify/require"
)

// captureReporter records every Fact reported to it.
type captureReporter struct {
	mu    sync.Mutex
	facts []observability.Fact
}

func (r *captureReporter) Report(f observability.Fact) {
	r.mu.Lock()
	r.facts = append(r.facts, f)
	r.mu.Unlock()
}

func (r *captureReporter) sawJobPhase(p observability.Phase) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, f := range r.facts {
		if f.Kind == observability.KindJobState && f.Phase == p {
			return true
		}
	}

	return false
}

// TestBindReporterRoutesJobFacts (SRD-041 M4): once the engine binds its Reporter,
// the dispatcher's job-lifecycle facts flow to it (Enqueued here); a nil bind is
// ignored (the echo-only default is kept, non-nil-Reporter invariant).
func TestBindReporterRoutesJobFacts(t *testing.T) {
	d := localdispatcher.New(clocktest.New(base), time.Minute)

	d.BindReporter(nil) // ignored — the default echo Reporter is kept

	rep := &captureReporter{}
	d.BindReporter(rep)

	require.NoError(t, d.Enqueue(context.Background(), newJob("j1", "charge")))

	require.True(t, rep.sawJobPhase(observability.PhaseEnqueued),
		"the bound Reporter receives the JobState/Enqueued fact")
}
