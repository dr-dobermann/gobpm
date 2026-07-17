package exec

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// callOutcomeTrigger is the internal trigger of the call-completion delivery —
// not a BPMN trigger; it never leaves the engine.
const callOutcomeTrigger flow.EventTrigger = "gobpm:call-outcome"

// CallOutcome is the synthetic completion the instance loop delivers to a parked
// Call Activity track when its child instance ends (the job/scope completion
// idiom). It carries the terminal fault (nil on a normal completion — the
// declared outputs are already committed into the caller scope by the loop
// before delivery); a non-nil err is returned by CallActivity.Exec so the
// caller track faults and the §2.6 error chain catches it at the node
// (SRD-050 FR-7/FR-8). Implements flow.EventDefinition so it rides the parked
// track's event channel exactly like a WorkerOutcome.
type CallOutcome struct {
	err error
	foundation.BaseElement
}

// NewCallOutcome mints a call-completion carrying err (nil = a normal
// completion, the caller track then just selects its outgoing flows).
func NewCallOutcome(err error) *CallOutcome {
	return &CallOutcome{BaseElement: *foundation.MustBaseElement(), err: err}
}

// Err returns the child's terminal fault, or nil on a normal completion.
func (o *CallOutcome) Err() error { return o.err }

// Type returns the internal call-completion trigger.
func (o *CallOutcome) Type() flow.EventTrigger { return callOutcomeTrigger }

// GetItemsList returns no payload — the outputs are committed into the caller
// scope loop-side before delivery, so the outcome itself carries only the fault.
func (o *CallOutcome) GetItemsList() []*data.ItemDefinition { return nil }

// ProcessInvoker launches a registered process as a CHILD instance on behalf of
// a Call Activity (ADR-023 v.1 §2.7): the reuse boundary. It is implemented by
// the engine (the thresher) and consumed by the instance loop — never by node
// code, so a node cannot launch a process outside the loop's call protocol
// (§4.1). An Instance carries a nil invoker for a library embedder without a
// thresher; a call then fails fast with a classified no-invoker error.
type ProcessInvoker interface {
	// InvokeProcess resolves the callable (Version 0 = latest-at-launch, else
	// the pinned 1-based version), binds the call's Inputs into the child's
	// root scope, launches the child, and returns its watch handle. A missing
	// key/version is a classified error that fails the CALL, not the engine.
	InvokeProcess(ctx context.Context, call ProcessCall) (ChildProcess, error)
}

// ProcessCall is the resolved request the loop hands the invoker for one Call
// Activity execution: which process to launch (Key + Version), the inputs to
// seed into its root scope (already resolved and cloned at the caller's scope —
// the isolation contract), and the linkage that stitches the child's trace back
// to the caller (ParentInstanceID + CallNodeID).
type ProcessCall struct {
	Key              string
	ParentInstanceID string
	CallNodeID       string
	Inputs           []data.Data
	Version          int
}

// ChildProcess is the caller loop's watch handle onto a launched child instance.
// The loop starts one watcher goroutine that blocks on Done, then reads Outputs
// (on normal completion) or Failed (on an abnormal terminal) and resumes the
// parked caller track. Terminate ends the child when the call's episode dies
// (the cancel cascade, ADR-023 §2.7).
type ChildProcess interface {
	// ID returns the child instance id.
	ID() string

	// Version returns the RESOLVED 1-based version the call bound — the pinned
	// version, or the concrete latest-at-launch version for a version-0 call.
	// The caller records it on the call fact (the audit point, ADR-023 §6).
	Version() int

	// Done is closed when the child reaches a terminal state (Completed or
	// Terminated) — a guaranteed, never-dropped signal.
	Done() <-chan struct{}

	// Failed reports the child's terminal fault: the terminal error if the
	// child ended abnormally, else nil (a normal completion, or a plain
	// cancellation that carried no fault). Read after Done is closed.
	Failed() error

	// Outputs reads the named data from the child's root scope after
	// completion — the declared Output parameters of the Call Activity, the
	// call contract's return values. A missing name is a classified error.
	Outputs(names []string) ([]data.Data, error)

	// Terminate ends the child (the cancel cascade). Idempotent.
	Terminate()
}
