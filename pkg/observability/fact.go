package observability

import "time"

// Kind classifies an observable engine event by object class (ADR-013 v.2
// §2.6). It is an OPEN vocabulary — a consumer must tolerate unknown values,
// since kinds and their phases grow additively. A named type (not bare string)
// so a wrong literal cannot compile into an event and the vocabulary is
// discoverable.
type Kind string

// The canonical observable-event kinds — one per major object class. These
// string values are the single source of truth for the observability
// vocabulary — engine, event hub, dispatcher, and instance loop all emit Facts
// carrying one of these kinds; there is no separate public alias to keep in sync.
const (
	KindEngineState      Kind = "EngineState"      // Thresher lifecycle
	KindHubState         Kind = "HubState"         // EventHub lifecycle
	KindProcessLifecycle Kind = "ProcessLifecycle" // process registration
	KindInstanceState    Kind = "InstanceState"    // instance lifecycle
	KindNodeProgress     Kind = "NodeProgress"     // a track's node execution phase
	KindGatewayDecision  Kind = "GatewayDecision"  // the chosen branch(es)
	KindEventFlow        Kind = "EventFlow"        // event registration/fire/delivery
	KindCorrelation      Kind = "Correlation"      // conversation-key decisions
	KindJobState         Kind = "JobState"         // external-worker job lifecycle
	KindTaskState        Kind = "TaskState"        // user-task lifecycle
	KindBoundary         Kind = "Boundary"         // boundary-event arm/fire/disarm
	KindFault            Kind = "Fault"            // BPMN error / fault
	KindEscalation       Kind = "Escalation"       // escalation throw/catch (non-fault, SRD-058)
	KindCompensation     Kind = "Compensation"     // completion-ledger lifecycle + compensation runs (ADR-026, SRD-059)
	KindRules            Kind = "Rules"            // decision evaluation on the Business Rule Engine (SRD-060)
	KindDataChange       Kind = "DataChange"       // data-element change (observer-only)
	KindScope            Kind = "Scope"            // nested-scope lifecycle (SRD-049)
	KindCall             Kind = "Call"             // call-activity lifecycle (SRD-050)
)

// Phase names the transition within a Kind (ADR-013 v.2 §2.6). Open and
// additive, per-kind; a named type for the same reasons as Kind. The ⏳
// reserved slots (Paused/Resumed, Incident) are declared where the ADR reserves
// them, so listeners see stable names when the subsystems land.
type Phase string

// The phase values, grouped by owning kind via trailing labels. Some phases are
// reused across kinds (Completed for instance/node/job/task; Fired for event and
// boundary; Registered/Unregistered for process and event), so they are declared
// once. The ⏳ trailing marks a slot reserved for a not-yet-landed subsystem.
const (
	PhaseStarting Phase = "Starting" // EngineState / HubState
	PhaseStarted  Phase = "Started"
	PhasePaused   Phase = "Paused"  // engine live; hub ⏳
	PhaseResumed  Phase = "Resumed" // ⏳ (resuming re-emits Started meanwhile)
	PhaseStopping Phase = "Stopping"
	PhaseStopped  Phase = "Stopped"

	PhaseRegistered        Phase = "Registered" // ProcessLifecycle / EventFlow
	PhaseUnregistered      Phase = "Unregistered"
	PhaseVersionSuperseded Phase = "VersionSuperseded"

	PhaseCreated     Phase = "Created" // InstanceState (Failed is phase-only)
	PhaseActive      Phase = "Active"
	PhaseTerminating Phase = "Terminating"
	PhaseCompleted   Phase = "Completed" // reused: instance / node / job / task
	PhaseTerminated  Phase = "Terminated"
	PhaseFailed      Phase = "Failed" // reused: instance / node

	PhaseEntered   Phase = "Entered" // NodeProgress (un-collapsed)
	PhaseExecuting Phase = "Executing"
	PhaseCanceled  Phase = "Canceled"
	PhaseMerged    Phase = "Merged"
	PhaseParked    Phase = "Parked"

	PhaseBranchesChosen Phase = "BranchesChosen" // GatewayDecision

	PhaseFired     Phase = "Fired" // EventFlow / Boundary
	PhaseDelivered Phase = "Delivered"
	PhaseDropped   Phase = "Dropped"

	PhaseOpened Phase = "Opened" // Scope (its other phases reuse
	// Completed/Terminated/Canceled)

	PhaseKeyAssociated Phase = "KeyAssociated" // Correlation
	PhaseMatched       Phase = "Matched"
	PhaseMismatched    Phase = "Mismatched"

	PhaseEnqueued         Phase = "Enqueued" // JobState
	PhaseLocked           Phase = "Locked"
	PhaseTechnicalFault   Phase = "TechnicalFault"
	PhaseBusinessError    Phase = "BusinessError"
	PhaseRetryScheduled   Phase = "RetryScheduled"
	PhaseRetriesExhausted Phase = "RetriesExhausted"
	PhaseLockReclaimed    Phase = "LockReclaimed"
	PhaseIncident         Phase = "Incident" // ⏳

	PhaseAnnounced Phase = "Announced" // TaskState
	PhaseTaken     Phase = "Taken"
	PhaseWithdrawn Phase = "Withdrawn"

	PhaseArmed    Phase = "Armed" // Boundary
	PhaseDisarmed Phase = "Disarmed"

	PhaseThrown   Phase = "Thrown" // Fault / Escalation
	PhaseCaught   Phase = "Caught"
	PhaseUncaught Phase = "Uncaught"
	// PhaseUnresolved: an escalation reached the scope-chain root with no
	// matching catcher (SRD-058 FR-4). Non-fault — the escalation analog of the
	// fault's Uncaught, but execution continues; logged, never silently dropped.
	PhaseUnresolved Phase = "Unresolved" // Escalation / Compensation

	// The completion-ledger lifecycle (ADR-026 §2.7, SRD-059 NFR-3): an entry is
	// recorded Eligible at its activity's Completed, Folded when a completed
	// child scope's ledger reparents into the enclosing scope's, and Discarded
	// when the enclosing scope finishes with the entry never compensated — the
	// normal end of the eligibility window. The observer stream is the ledger's
	// audit log: recorded → folded* → consumed | discarded.
	PhaseEligible  Phase = "Eligible" // Compensation
	PhaseFolded    Phase = "Folded"
	PhaseDiscarded Phase = "Discarded"
	// Compensating/Compensated fill the ADR-013 v.2 reserved slots (SRD-059
	// FR-6): a handler invocation opens Compensating and closes Compensated —
	// or the activity-side `Compensating → Failed` when the handler itself
	// fails (a real Error-chain fault).
	PhaseCompensating Phase = "Compensating"
	PhaseCompensated  Phase = "Compensated"

	// PhaseEvaluated: a Business Rule Task's decision call returned and its
	// result committed (SRD-060 FR-6). Failure reuses PhaseFailed with the
	// decision-level details; the task failure itself still rides KindFault.
	PhaseEvaluated Phase = "Evaluated" // Rules

	PhaseValueAdded   Phase = "Value_Added" // DataChange (= data.ChangeType)
	PhaseValueUpdated Phase = "Value_Updated"
	PhaseValueDeleted Phase = "Value_Deleted"
)

// The canonical detail-attribute keys (ADR-022 v.1 §2.5 vocabulary, ADR-013 v.2
// §2.9). Untyped named string constants: one set of keys serves both an
// Fact's Details map and a slog echo's key/value args — so the observer
// stream and the operator log correlate on the same names.
const (
	AttrInstanceID        = "instance_id"
	AttrTrackID           = "track_id"
	AttrNodeID            = "node_id"
	AttrNodeName          = "node_name"
	AttrProcessID         = "process_id"
	AttrStartNodeID       = "start_node_id"
	AttrTaskID            = "task_id"
	AttrJobID             = "job_id"
	AttrWorkerID          = "worker_id"
	AttrTopic             = "topic"
	AttrEventDefinitionID = "event_definition_id"
	AttrWaiterID          = "waiter_id"
	AttrSignal            = "signal"
	AttrMessageName       = "message_name"
	AttrCorrelationKey    = "correlation_key"
	AttrCorrelationValue  = "correlation_value"
	AttrError             = "error"
	AttrEscalation        = "escalation"
	// Decision evaluation on the Business Rule Engine (SRD-060 FR-6). Names
	// and counts only — never decision payload values (the masking rule).
	AttrDecisionRef    = "decision_ref"
	AttrImplementation = "implementation"
	AttrRowCount       = "row_count"
	AttrResultVariable = "result_variable"
	// AttrOrdinal (SRD-059): a completion-ledger entry's 0-based completion
	// order within its scope — the reverse-compensation order's authority.
	AttrOrdinal     = "ordinal"
	AttrChosenFlows = "chosen_flows"
	AttrVersion     = "version"
	AttrAttempts    = "attempts"
	AttrBackoff     = "backoff"
	AttrDataPath    = "data_path"
	AttrScopePath   = "scope_path"

	// AttrLoopCounter (SRD-054): the 0-based iteration ordinal a looped composite
	// activity's scope carries, so each Standard-Loop pass is individually
	// observable on its scope facts.
	AttrLoopCounter = "loop_counter"

	// Call-activity linkage (SRD-050): stamped on every fact a CHILD instance
	// emits, stitching its trace back to the caller across the reuse boundary.
	AttrParentInstanceID   = "parent_instance_id"
	AttrCallActivityNodeID = "call_activity_node_id"

	// Call-activity facts (SRD-050 FR-10): emitted by the caller — the called
	// process key, the RESOLVED version bound (the latest-at-launch audit
	// point), and the launched child instance id.
	AttrCalledKey       = "called_key"
	AttrCalledVersion   = "called_version"
	AttrChildInstanceID = "child_instance_id"
)

// Fact is the canonical observable engine event (ADR-013 v.2 §2.6/§2.9): a
// failure or a major-object lifecycle transition. It is the ONE event type
// every emitter produces — engine, event hub, dispatcher, instance loop, and
// pkg/model nodes — so there is no cross-package mapping between an internal and
// a public shape.
//
// It carries identity, phase and timing only — never process payload values
// (the masking rule, ADR-010/011). Kind-specific identifiers live in Details,
// keyed by the Attr* vocabulary above.
type Fact struct {
	At       time.Time
	Details  map[string]string
	Kind     Kind
	Phase    Phase
	NodeID   string
	NodeName string
}

// Reporter is the single producer behind every observable event (ADR-013 v.2
// §2.7): its Report writes the operator-log echo AND fans the event out to the
// registered observers. Report MUST be non-blocking for the caller — it runs on
// the execution hot path — so a slow observer drops events rather than stalling
// the engine, and the log echo is a plain synchronous logger call.
//
// The engine's default sink is echo-only (NewEchoReporter) — never a silent no-op,
// so the visible-by-default posture (ADR-022 §2.6) holds even before any
// observer registers.
type Reporter interface {
	Report(ev Fact)
}
