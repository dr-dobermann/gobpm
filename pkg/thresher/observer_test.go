package thresher_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

// collector records every event it receives (concurrency-safe).
type collector struct {
	mu     sync.Mutex
	events []observability.Fact
}

func (c *collector) OnFact(ev observability.Fact) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, ev)
}

func (c *collector) sawCompleted() bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, e := range c.events {
		if e.Kind == observability.KindInstanceState &&
			e.Phase == observability.PhaseCompleted {
			return true
		}
	}

	return false
}

func (c *collector) sawNodeProgress() bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, e := range c.events {
		if e.Kind == observability.KindNodeProgress {
			return true
		}
	}

	return false
}

// blockingObserver blocks in OnFact until release is closed.
type blockingObserver struct{ release chan struct{} }

func (b *blockingObserver) OnFact(observability.Fact) { <-b.release }

// panicObserver panics on every event.
type panicObserver struct{}

func (panicObserver) OnFact(observability.Fact) { panic("observer boom") }

// chainProcess builds start -> task0(sleep firstSleep) -> task1..N-1 -> end. The
// first task's sleep gives a late observer time to register; the remaining fast
// tasks then emit far more than the buffer holds.
func chainProcess(t *testing.T, id string, n int, firstSleep time.Duration) *process.Process {
	t.Helper()

	proc, err := process.New(id)
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	require.NoError(t, proc.Add(start))

	prev := flow.Element(start)
	for i := range n {
		sleep := time.Duration(0)
		if i == 0 {
			sleep = firstSleep
		}

		task, err := activities.NewServiceTask(fmt.Sprintf("task-%d", i),
			nopOp(t, fmt.Sprintf("op-%d", i), sleep), activities.WithoutParams())
		require.NoError(t, err)
		require.NoError(t, proc.Add(task))
		link(t, prev, task)
		prev = task
	}

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)
	require.NoError(t, proc.Add(end))
	link(t, prev, end)

	return proc
}

// TestObserverReceivesLifecycleEvents verifies an observer receives instance
// lifecycle and node-progress events (FR-7, FR-8). The service task sleeps so
// the observer registers before the bulk of the run.
func TestObserverReceivesLifecycleEvents(t *testing.T) {
	proc := linearProcess(t, "obs-recv", 300*time.Millisecond)
	th, cancel := runEngine(t, proc)
	defer cancel()

	h, err := th.StartLatest(proc.ID())
	require.NoError(t, err)

	c := &collector{}
	sub := h.Observe(c)

	ctx, cc := context.WithTimeout(context.Background(), 3*time.Second)
	defer cc()

	state, err := h.WaitCompletion(ctx)
	require.NoError(t, err)
	require.Equal(t, thresher.StateCompleted, state)

	sub.Cancel() // deregisters and drains buffered events

	require.True(t, c.sawCompleted(), "observer should see the Completed event")
	require.True(t, c.sawNodeProgress(), "observer should see node-progress events")
}

// TestSlowObserverDropsNeverBlocks verifies that a blocked observer never stalls
// the engine and that the overflow is counted (FR-9).
func TestSlowObserverDropsNeverBlocks(t *testing.T) {
	proc := chainProcess(t, "obs-drop", 80, 300*time.Millisecond)
	th, cancel := runEngine(t, proc)
	defer cancel()

	h, err := th.StartLatest(proc.ID())
	require.NoError(t, err)

	blk := &blockingObserver{release: make(chan struct{})}
	sub := h.Observe(blk)

	ctx, cc := context.WithTimeout(context.Background(), 5*time.Second)
	defer cc()

	// The instance completes even though the observer is blocked.
	state, err := h.WaitCompletion(ctx)
	require.NoError(t, err)
	require.Equal(t, thresher.StateCompleted, state)

	// Emission past the buffer was dropped while the observer was blocked.
	require.Positive(t, sub.Dropped())

	close(blk.release) // let the drain finish so Cancel can return
	sub.Cancel()
}

// TestObserverPanicRecovered verifies a panicking observer neither crashes the
// engine nor starves a healthy peer observer (FR-9).
func TestObserverPanicRecovered(t *testing.T) {
	proc := linearProcess(t, "obs-panic", 300*time.Millisecond)
	th, cancel := runEngine(t, proc)
	defer cancel()

	h, err := th.StartLatest(proc.ID())
	require.NoError(t, err)

	psub := h.Observe(panicObserver{})
	c := &collector{}
	csub := h.Observe(c)

	ctx, cc := context.WithTimeout(context.Background(), 3*time.Second)
	defer cc()

	state, err := h.WaitCompletion(ctx)
	require.NoError(t, err)
	require.Equal(t, thresher.StateCompleted, state)

	psub.Cancel()
	csub.Cancel()

	require.True(t, c.sawCompleted(),
		"a healthy observer still receives events despite a panicking peer")
}
