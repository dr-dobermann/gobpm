package tasks

import (
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// workerOutcomeTrigger is the internal sentinel trigger of a WorkerOutcome. It
// is never matched against BPMN triggers — the loop delivers it straight to the
// parked ServiceTask track, bypassing the EventHub and correlation.
const workerOutcomeTrigger flow.EventTrigger = "ServiceTaskWorkerOutcome"

// OutcomeKind classifies a WorkerOutcome into one of the four ADR-021 §2.6 kinds.
// The parked ServiceTask dispatches on it at resume (SRD-037 §3.5).
type OutcomeKind uint8

const (
	// OutcomeComplete is a success — bind Output, complete.
	OutcomeComplete OutcomeKind = iota
	// OutcomeBpmnError is a worker-declared Business Error — raise BpmnCode, caught
	// by an Error boundary (interrupting).
	OutcomeBpmnError
	// OutcomeStatus is a worker-declared Business Status — write StatusValue to the
	// WithStatus variable, complete normally.
	OutcomeStatus
	// OutcomeFault is a raw fault — the engine ErrorMapper classifies Fault{code,body}
	// at resume.
	OutcomeFault
)

// outcomeKindNames is the kind→name table, keyed by the constant so it stays
// correct if the iota block is reordered. Keep it in sync with that block.
var outcomeKindNames = [...]string{
	OutcomeComplete:  "complete",
	OutcomeBpmnError: "bpmnError",
	OutcomeStatus:    "status",
	OutcomeFault:     "fault",
}

// String returns the outcome-kind name for logging.
func (k OutcomeKind) String() string {
	if int(k) >= len(outcomeKindNames) {
		return "unknown"
	}

	return outcomeKindNames[k]
}

// WorkerOutcome is the synthetic event a worker's report rides back into the
// instance loop: it implements flow.EventDefinition, so it flows through the
// parked track's event channel exactly like a UserTask completion (ADR-021
// §2.4, SRD-036 §3.2). Its Kind selects which field is meaningful. It never
// reaches the EventHub or correlation, so its Type is an internal sentinel and it
// exposes no ItemDefinitions.
type WorkerOutcome struct {
	status   data.Value
	output   *data.ItemDefinition
	fault    Fault
	bpmnCode string
	bpmnMsg  string
	jobID    JobID
	foundation.BaseElement
	kind OutcomeKind
}

// NewWorkerComplete builds a successful outcome for job jobID carrying output
// (nil if the operation produced none).
func NewWorkerComplete(jobID JobID, output *data.ItemDefinition) *WorkerOutcome {
	return &WorkerOutcome{
		BaseElement: *foundation.MustBaseElement(),
		jobID:       jobID,
		kind:        OutcomeComplete,
		output:      output,
	}
}

// NewWorkerBpmnError builds a worker-declared Business Error outcome: the engine
// raises code (message is an optional diagnostic), caught by an Error boundary.
func NewWorkerBpmnError(jobID JobID, code, message string) *WorkerOutcome {
	return &WorkerOutcome{
		BaseElement: *foundation.MustBaseElement(),
		jobID:       jobID,
		kind:        OutcomeBpmnError,
		bpmnCode:    code,
		bpmnMsg:     message,
	}
}

// NewWorkerStatus builds a worker-declared Business Status outcome carrying value
// (written to the ServiceTask's WithStatus variable).
func NewWorkerStatus(jobID JobID, value data.Value) *WorkerOutcome {
	return &WorkerOutcome{
		BaseElement: *foundation.MustBaseElement(),
		jobID:       jobID,
		kind:        OutcomeStatus,
		status:      value,
	}
}

// NewWorkerFault builds a raw-fault outcome for job jobID; the engine ErrorMapper
// classifies fault's {code, body} at resume.
func NewWorkerFault(jobID JobID, fault Fault) *WorkerOutcome {
	return &WorkerOutcome{
		BaseElement: *foundation.MustBaseElement(),
		jobID:       jobID,
		kind:        OutcomeFault,
		fault:       fault,
	}
}

// JobID returns the job this outcome reports.
func (o *WorkerOutcome) JobID() JobID { return o.jobID }

// Kind returns the outcome's classification.
func (o *WorkerOutcome) Kind() OutcomeKind { return o.kind }

// Output returns the operation result item on a completion (nil otherwise).
func (o *WorkerOutcome) Output() *data.ItemDefinition { return o.output }

// BpmnError returns the worker-declared business-error code and message (empty on
// other kinds).
func (o *WorkerOutcome) BpmnError() (code, message string) {
	return o.bpmnCode, o.bpmnMsg
}

// StatusValue returns the worker-declared business-status value (nil on other
// kinds).
func (o *WorkerOutcome) StatusValue() data.Value { return o.status }

// Fault returns the raw fault (zero on other kinds); the engine ErrorMapper
// classifies its {code, body}.
func (o *WorkerOutcome) Fault() Fault { return o.fault }

// Type returns the internal worker-outcome sentinel.
func (o *WorkerOutcome) Type() flow.EventTrigger { return workerOutcomeTrigger }

// GetItemsList returns nil — a WorkerOutcome carries its data via its kind-specific
// accessors, not ItemDefinitions.
func (o *WorkerOutcome) GetItemsList() []*data.ItemDefinition { return nil }

var _ flow.EventDefinition = (*WorkerOutcome)(nil)
