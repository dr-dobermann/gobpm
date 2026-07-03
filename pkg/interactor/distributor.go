package interactor

import "context"

// TaskDistributor is the embedder-provided boundary that surfaces human tasks
// (ADR-020 §2.2), injected into the engine like MessageBroker or Clock. The
// engine calls it to announce a newly parked UserTask and to retract one that is
// no longer completable; it does NOT drive execution — the human acts through the
// engine's Take/Complete entry points, which the engine authorizes.
type TaskDistributor interface {
	// Distribute announces a parked UserTask as available for human work.
	Distribute(ctx context.Context, task TaskInfo) error

	// Withdraw retracts a task that is no longer completable — it was completed,
	// or its activity was canceled (e.g. an interrupting boundary event fired).
	Withdraw(ctx context.Context, taskID string) error
}

// nopDistributor is the default TaskDistributor: it announces nothing. Tasks
// still park and remain completable by id — an embedder that wants an inbox
// injects its own (e.g. the console distributor) via WithTaskDistributor. Being
// non-nil, it frees the engine from nil-checking the boundary.
type nopDistributor struct{}

func (nopDistributor) Distribute(context.Context, TaskInfo) error { return nil }

func (nopDistributor) Withdraw(context.Context, string) error { return nil }

// NopDistributor returns the no-op TaskDistributor used as the engine default.
func NopDistributor() TaskDistributor {
	return nopDistributor{}
}
