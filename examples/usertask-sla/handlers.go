package main

import (
	"context"
	"fmt"
	"sync"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
)

// notices records which SLA marks actually fired, in the order they fired.
//
// It records from INSIDE the notification tasks, synchronously with the run,
// rather than from an engine observer: observer facts are delivered
// best-effort and asynchronously (ADR-013), so one can still be in flight when
// WaitCompletion returns, and an example that asserted on them would be flaky
// for reasons that have nothing to do with timers.
type notices struct {
	seen []string
	mu   sync.Mutex
}

// mark appends one fired notification.
func (n *notices) mark(what string) {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.seen = append(n.seen, what)
}

// fired returns a copy of the marks recorded so far, in order.
func (n *notices) fired() []string {
	n.mu.Lock()
	defer n.mu.Unlock()

	return append([]string(nil), n.seen...)
}

// noticeTask builds the ServiceTask a boundary routes to: it records the mark
// and prints it, so the run is both observable and checkable.
func noticeTask(
	rec *notices, id, msg string,
) (*activities.ServiceTask, error) {
	op, err := gooper.New(id,
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition,
		) (*data.ItemDefinition, error) {
			rec.mark(id)
			fmt.Println("  ⏰", msg)

			return nil, nil
		})
	if err != nil {
		return nil, fmt.Errorf("notice op %q: %w", id, err)
	}

	task, err := activities.NewServiceTask(id, op, activities.WithoutParams())
	if err != nil {
		return nil, fmt.Errorf("notice task %q: %w", id, err)
	}

	return task, nil
}
