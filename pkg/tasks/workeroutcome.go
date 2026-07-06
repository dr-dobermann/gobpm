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

// WorkerOutcome is the synthetic event a worker's report rides back into the
// instance loop: it implements flow.EventDefinition, so it flows through the
// parked track's event channel exactly like a UserTask completion (ADR-021
// §2.4, SRD-036 §3.2). Exactly one of cause / output is meaningful — a Complete
// carries the output item, a Fail carries the cause. It never reaches the
// EventHub or correlation, so its Type is an internal sentinel and it exposes no
// ItemDefinitions.
type WorkerOutcome struct {
	cause  error
	output *data.ItemDefinition
	jobID  JobID
	foundation.BaseElement
}

// NewWorkerComplete builds a successful outcome for job jobID carrying output
// (nil if the operation produced none).
func NewWorkerComplete(jobID JobID, output *data.ItemDefinition) *WorkerOutcome {
	return &WorkerOutcome{
		BaseElement: *foundation.MustBaseElement(),
		jobID:       jobID,
		output:      output,
	}
}

// NewWorkerFail builds a technical-fault outcome for job jobID carrying cause.
func NewWorkerFail(jobID JobID, cause error) *WorkerOutcome {
	return &WorkerOutcome{
		BaseElement: *foundation.MustBaseElement(),
		jobID:       jobID,
		cause:       cause,
	}
}

// JobID returns the job this outcome reports.
func (o *WorkerOutcome) JobID() JobID { return o.jobID }

// Output returns the operation result item on a completion (nil on a fault, or
// when the operation produced no output).
func (o *WorkerOutcome) Output() *data.ItemDefinition { return o.output }

// Cause returns the technical-fault cause (nil on a completion).
func (o *WorkerOutcome) Cause() error { return o.cause }

// Type returns the internal worker-outcome sentinel.
func (o *WorkerOutcome) Type() flow.EventTrigger { return workerOutcomeTrigger }

// GetItemsList returns nil — a WorkerOutcome carries its data via Output, not
// ItemDefinitions.
func (o *WorkerOutcome) GetItemsList() []*data.ItemDefinition { return nil }

var _ flow.EventDefinition = (*WorkerOutcome)(nil)
