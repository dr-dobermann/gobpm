package main

import (
	"context"
	"sync"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/interactor"
)

// inbox is a minimal TaskDistributor: it just remembers the announced task id.
// The point for THIS example is that the inbox outlives the instance — a
// parked task is held here, not in the engine's memory, which is exactly why
// the instance can release its goroutines while the task is pending.
type inbox struct {
	taskID string
	mu     sync.Mutex
}

func newInbox() *inbox {
	return &inbox{}
}

func (i *inbox) Distribute(_ context.Context, task interactor.TaskInfo) error {
	i.mu.Lock()
	i.taskID = task.TaskID
	i.mu.Unlock()

	return nil
}

func (i *inbox) Withdraw(context.Context, string) error {
	return nil
}

// awaitTask returns the announced task id once it arrives.
//
// Note it does NOT change across a dehydration cycle: a rehydrated task is
// re-announced under its RECORDED id, so a reference a human (or a UI) is
// holding stays valid.
func (i *inbox) awaitTask(within time.Duration) (string, bool) {
	deadline := time.Now().Add(within)

	for time.Now().Before(deadline) {
		i.mu.Lock()
		id := i.taskID
		i.mu.Unlock()

		if id != "" {
			return id, true
		}

		time.Sleep(5 * time.Millisecond)
	}

	return "", false
}

// actor is the human acting on the task.
type actor struct{ id string }

func (a actor) UserID() string   { return a.id }
func (a actor) Groups() []string { return nil }
