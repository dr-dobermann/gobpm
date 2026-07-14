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
	KindDataChange       Kind = "DataChange"       // data-element change (observer-only)
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

	PhaseThrown   Phase = "Thrown" // Fault
	PhaseCaught   Phase = "Caught"
	PhaseUncaught Phase = "Uncaught"

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
	AttrChosenFlows       = "chosen_flows"
	AttrVersion           = "version"
	AttrAttempts          = "attempts"
	AttrBackoff           = "backoff"
	AttrDataPath          = "data_path"
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
