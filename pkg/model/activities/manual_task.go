package activities

import (
	"context"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/exec"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/renv"
)

// ManualTask is a BPMN Manual Task — work performed without any IT system
// (§13.1, a non-operational element). Per Process Execution Conformance the
// engine MAY treat it as a no-op pass-through, and gobpm does: on activation the
// token flows straight to the outgoing sequence flow(s) with no distribution and
// no wait (ADR-020 §2.10).
type ManualTask struct {
	task
}

// NewManualTask creates a ManualTask with name and foundation/activity options.
func NewManualTask(
	name string,
	opts ...options.Option,
) (*ManualTask, error) {
	t, err := newTask(strings.TrimSpace(name), opts...)
	if err != nil {
		return nil, errs.New(
			errs.M("manual task building failed"),
			errs.C(errorClass, errs.BulidingFailed),
			errs.E(err))
	}

	return &ManualTask{task: *t}, nil
}

// ----------------------- flow.Node interface --------------------------------

// Node returns the ManualTask as a flow node.
func (mt *ManualTask) Node() flow.Node {
	return mt
}

// Clone returns a per-instance copy of the ManualTask (a fresh activity shell
// over the shared config).
func (mt *ManualTask) Clone() (flow.Node, error) {
	t, err := mt.clone()
	if err != nil {
		return nil, err
	}

	return &ManualTask{task: t}, nil
}

// ------------------------ flow.Task interface -------------------------------

// TaskType returns the task type for ManualTask.
func (mt *ManualTask) TaskType() flow.TaskType {
	return flow.ManualTask
}

// ----------------------exec.NodeExecutor interface --------------------------

// Exec is a no-op pass-through: a Manual Task is never executed by an IT system
// (BPMN §13.1), so it binds nothing and advances straight to its outgoing flows.
func (mt *ManualTask) Exec(
	ctx context.Context,
	re renv.RuntimeEnvironment,
) ([]*flow.SequenceFlow, error) {
	return mt.selectOutgoing(ctx, re)
}

// ----------------------------------------------------------------------------

// interfaces check
var (
	_ flow.Node         = (*ManualTask)(nil)
	_ flow.Task         = (*ManualTask)(nil)
	_ exec.NodeExecutor = (*ManualTask)(nil)
)
