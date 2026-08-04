package main

import (
	"context"
	"fmt"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/interactor"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// operator is the human the slow driver acts as.
type operator struct{}

func (operator) UserID() string   { return "operator" }
func (operator) Groups() []string { return nil }

// slowOperator is a TaskDistributor that deliberately takes its time: it holds
// each announced task for `hold` before taking, claiming and completing it.
//
// That delay is the example's subject. The approval is meant to breach its SLA,
// so every mark fires while the task is still open — which is exactly what a
// NON-interrupting boundary is for. With an interrupting one the first mark
// would have cancelled the very work it was warning about.
type slowOperator struct {
	eng  *thresher.Thresher
	hold time.Duration
}

// Bind attaches the engine the driver acts on. It is set after construction
// because the engine needs the distributor at New time.
func (s *slowOperator) Bind(t *thresher.Thresher) { s.eng = t }

// Distribute is the engine's announcement that a UserTask is waiting. It
// returns immediately — the engine must not be blocked by a human — and does
// the work on its own goroutine.
func (s *slowOperator) Distribute(
	_ context.Context, task interactor.TaskInfo,
) error {
	go s.workOn(task.TaskID)

	return nil
}

// Withdraw is called when a task is no longer completable. Nothing to undo
// here: the goroutine's Take will simply fail and report.
func (s *slowOperator) Withdraw(context.Context, string) error { return nil }

// workOn performs the human's side of the protocol after the hold: Take to see
// the task, Claim for exclusive hold, Complete to release the token. Claiming
// is required — only the holder may complete (ADR-020 §2.5).
func (s *slowOperator) workOn(taskID string) {
	fmt.Printf("  ⏳ operator received the invoice, will take %s over it\n",
		s.hold)

	time.Sleep(s.hold)

	ctx := context.Background()

	if _, err := s.eng.Take(ctx, taskID, operator{}); err != nil {
		fmt.Println("  ✗ take:", err)

		return
	}

	if err := s.eng.Claim(ctx, taskID, operator{}); err != nil {
		fmt.Println("  ✗ claim:", err)

		return
	}

	if err := s.eng.Complete(ctx, taskID, operator{}, nil); err != nil {
		fmt.Println("  ✗ complete:", err)

		return
	}

	fmt.Println("  ✓ operator approved the invoice — late, but approved")
}

var _ interactor.TaskDistributor = (*slowOperator)(nil)
