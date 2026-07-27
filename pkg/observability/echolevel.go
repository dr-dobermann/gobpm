package observability

import "log/slog"

// kindLevel is the operator-log echo level per event kind (ADR-013 v.2 §2.6
// "log echo" column, ADR-022 v.1 §2.4 level semantics): lifecycle milestones
// echo at Info, flow tracing at Debug. A kind absent from this table is an
// unclassified/future kind, which echoLevel surfaces loudly at Error.
var kindLevel = map[Kind]slog.Level{
	KindEngineState:      slog.LevelInfo,
	KindHubState:         slog.LevelInfo,
	KindProcessLifecycle: slog.LevelInfo,
	KindInstanceState:    slog.LevelInfo,
	KindTaskState:        slog.LevelInfo,
	KindScope:            slog.LevelInfo,
	KindCall:             slog.LevelInfo,
	KindNodeProgress:     slog.LevelDebug,
	KindGatewayDecision:  slog.LevelDebug,
	KindEventFlow:        slog.LevelDebug,
	KindCorrelation:      slog.LevelDebug,
	KindBoundary:         slog.LevelDebug,
	KindJobState:         slog.LevelDebug,
	KindFault:            slog.LevelDebug,
	KindEscalation:       slog.LevelDebug,
	KindCompensation:     slog.LevelDebug,
	KindRules:            slog.LevelDebug,
	KindScript:           slog.LevelDebug,
	// A Data Store access is engine-global shared state — operationally
	// significant enough to log (unlike the observer-only per-instance
	// KindDataObject below), but flow-level, so Debug (SRD-068).
	KindDataStore: slog.LevelDebug,
}

// kindNoEcho lists kinds that never reach the operator log — the observer stream
// carries them alone. KindDataChange qualifies: its ~ten-writes-per-node volume
// would drown flow tracing even at Debug (the §2.10 flood guard).
var kindNoEcho = map[Kind]bool{
	KindDataChange: true,
	// KindDataObject is per-instance Data Object churn (SRD-063) — same
	// ~per-node-write volume as KindDataChange, so it too would drown flow
	// tracing; the observer stream carries it alone.
	KindDataObject: true,
}

// phaseKey addresses a per-phase override within a kind.
type phaseKey struct {
	kind  Kind
	phase Phase
}

// phaseOverride escalates specific failure/degradation phases above their kind's
// default: an instance or an uncaught fault surfaces at Error, a job that
// exhausted its retries or lost its lock at Warn (ADR-013 v.2 §2.6).
var phaseOverride = map[phaseKey]slog.Level{
	{KindInstanceState, PhaseFailed}:      slog.LevelError,
	{KindFault, PhaseUncaught}:            slog.LevelError,
	{KindJobState, PhaseRetriesExhausted}: slog.LevelWarn,
	{KindJobState, PhaseLockReclaimed}:    slog.LevelWarn,
	// An unresolved escalation is not a fault, but it is a likely modeling
	// mistake (a throw with no reachable catcher) — surface it at Warn so it is
	// logged, never silently dropped (SRD-058 FR-4).
	{KindEscalation, PhaseUnresolved}: slog.LevelWarn,
	// A decision-evaluation failure is operator-relevant context for the fault
	// that follows it (SRD-060 FR-6).
	{KindRules, PhaseFailed}: slog.LevelWarn,
	// A runtime deploy is a governance milestone — the ProcessLifecycle
	// analog on the rules seam (SRD-069 FR-6).
	{KindRules, PhaseDeployed}: slog.LevelInfo,
	// A deferred checkpoint means durability is OFF while the instance
	// runs — operator-relevant degradation (SRD-070 FR-4/FR-8).
	{KindInstanceState, PhaseCheckpointDeferred}: slog.LevelWarn,
	// A script-execution failure — same posture (SRD-064 FR-5).
	{KindScript, PhaseFailed}: slog.LevelWarn,
	// Same rule for a compensation throw that resolved to nothing (SRD-059
	// FR-8): logged at Warn, never a fault, never silent.
	{KindCompensation, PhaseUnresolved}: slog.LevelWarn,
}

// loggable reports whether an event of this kind is echoed to the operator log
// at all — the log-INCLUSION responsibility, derived purely from the event kind.
// It is deliberately separate from echoLevel (which chooses the level): whether
// to log and at what level are two concerns, and the sink composes them.
func loggable(kind Kind) bool {
	return !kindNoEcho[kind]
}

// echoLevel returns the operator-log level for a loggable event — the
// level-SELECTION responsibility, a pure function of the event's kind and phase.
// A per-phase override wins, then the kind default; an unclassified kind echoes
// at Error, since an event we never mapped is a coding gap the engine's
// visible-by-default posture should surface loudly rather than bury at Debug.
func echoLevel(kind Kind, phase Phase) slog.Level {
	if lvl, over := phaseOverride[phaseKey{kind, phase}]; over {
		return lvl
	}

	if lvl, known := kindLevel[kind]; known {
		return lvl
	}

	return slog.LevelError
}
