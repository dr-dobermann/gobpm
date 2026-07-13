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
	KindNodeProgress:     slog.LevelDebug,
	KindGatewayDecision:  slog.LevelDebug,
	KindEventFlow:        slog.LevelDebug,
	KindCorrelation:      slog.LevelDebug,
	KindBoundary:         slog.LevelDebug,
	KindJobState:         slog.LevelDebug,
	KindFault:            slog.LevelDebug,
}

// kindNoEcho lists kinds that never reach the operator log — the observer stream
// carries them alone. KindDataChange qualifies: its ~ten-writes-per-node volume
// would drown flow tracing even at Debug (the §2.10 flood guard).
var kindNoEcho = map[Kind]bool{
	KindDataChange: true,
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
