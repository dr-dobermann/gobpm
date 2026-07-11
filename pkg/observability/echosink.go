package observability

import "log/slog"

// levelEcho maps an echo level to the Logger method that emits at it — a lookup
// table rather than a switch. echoLevel only ever returns these four levels, so
// the table is total and the sink never needs a fallback branch.
var levelEcho = map[slog.Level]func(Logger, string, ...any){
	slog.LevelDebug: Logger.Debug,
	slog.LevelInfo:  Logger.Info,
	slog.LevelWarn:  Logger.Warn,
	slog.LevelError: Logger.Error,
}

// Echo writes an observable event's operator-log echo to log, composing the two
// separate log-echo responsibilities: loggable (inclusion) and echoLevel
// (level). A non-loggable event (DataChange) or a nil logger writes nothing.
// It makes no observer-stream decision — a producer calls it for the log half of
// the fan-out only; the engine-scope producer reuses it for the same half.
func Echo(log Logger, ev ObsEvent) {
	if log == nil || !loggable(ev.Kind) {
		return
	}

	emit := levelEcho[echoLevel(ev.Kind, ev.Phase)]
	emit(log, echoMessage(ev), echoArgs(ev)...)
}

// echoMessage is the log line's message for an event: "<Kind> <Phase>", or just
// the kind when the event is phaseless.
func echoMessage(ev ObsEvent) string {
	if ev.Phase == "" {
		return string(ev.Kind)
	}

	return string(ev.Kind) + " " + string(ev.Phase)
}

// echoArgs flattens an event's identity and details into slog key/value args,
// reusing the Attr* keys so the operator log and the observer stream correlate
// on the same names (the one-vocabulary-two-channels point of §2.9).
func echoArgs(ev ObsEvent) []any {
	args := make([]any, 0, 4+2*len(ev.Details))
	if ev.NodeID != "" {
		args = append(args, AttrNodeID, ev.NodeID)
	}

	if ev.NodeName != "" {
		args = append(args, AttrNodeName, ev.NodeName)
	}

	for k, v := range ev.Details {
		args = append(args, k, v)
	}

	return args
}

// echoSink is the default ObsSink (ADR-013 v.2 §2.7): echo-only. It writes each
// loggable event to the operator log and fans out to no observer — the observer
// registry is layered on by the engine-scope producer in a later milestone. It
// is never a silent no-op, so the visible-by-default posture (ADR-022 §2.6)
// holds before any observer registers.
type echoSink struct {
	log Logger
}

// NewEchoSink returns the default echo-only sink bound to log. A nil log falls
// back to slog.Default(), so the sink is never silent.
func NewEchoSink(log Logger) ObsSink {
	if log == nil {
		log = slog.Default()
	}

	return &echoSink{log: log}
}

// Emit echoes a loggable event to the operator log (the ObsSink contract).
func (s *echoSink) Emit(ev ObsEvent) {
	Echo(s.log, ev)
}
