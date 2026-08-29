package main

import (
	"context"
	"fmt"
	"sync"

	"github.com/dr-dobermann/gobpm/pkg/interactor"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	hi "github.com/dr-dobermann/gobpm/pkg/model/hinteraction"
)

// reviewer is one acting human. Each instance of the fan-out is completed by
// the reviewer it was offered to, which is the whole point of the construct:
// three approvals, three people, at the same time.
type reviewer struct{ id string }

func (r reviewer) UserID() string   { return r.id }
func (r reviewer) Groups() []string { return nil }

// engine is the half of the Thresher an inbox needs.
type engine interface {
	Take(ctx context.Context, taskID string, a hi.Actor) (
		interactor.TaskView, error)
	Claim(ctx context.Context, taskID string, a hi.Actor) error
	Complete(ctx context.Context, taskID string, a hi.Actor,
		outputs []data.Data) error
}

// inbox stands in for the place people actually work: it is handed each task
// as the engine announces it, and answers it on that person's behalf.
//
// It records what it saw, so the run can assert that three DISTINCT tasks were
// offered — the property that was missing when N instances shared one identity
// and only one of them was addressable.
type inbox struct {
	eng  engine
	seen []string

	mu sync.Mutex
}

func newInbox() *inbox { return &inbox{} }

func (i *inbox) bind(e engine) { i.eng = e }

// Distribute answers each task on its own goroutine, as a real inbox would:
// the people are working at the same time.
func (i *inbox) Distribute(
	ctx context.Context, task interactor.TaskInfo,
) error {
	i.mu.Lock()
	i.seen = append(i.seen, task.TaskID)
	i.mu.Unlock()

	// The announcement carries the eligibility the engine RESOLVED for this
	// instance — so the inbox knows whose work it is without opening it, and
	// three instances of one activity name three different people.
	who := task.Eligible.Assignee.IDs
	if len(who) != 1 {
		fmt.Printf("  task %s names %d assignees, want 1\n",
			task.TaskID, len(who))

		return nil
	}

	go i.answer(context.WithoutCancel(ctx), task.TaskID, who[0])

	return nil
}

// Withdraw reports a task that is no longer completable.
func (i *inbox) Withdraw(_ context.Context, taskID string) error {
	fmt.Printf("  withdrawn: %s\n", taskID)

	return nil
}

// offered reports the task ids announced, in order.
func (i *inbox) offered() []string {
	i.mu.Lock()
	defer i.mu.Unlock()

	return append([]string{}, i.seen...)
}
