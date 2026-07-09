package tasks

// TrustMode governs where a worker-dispatched ServiceTask's policy bundle (output
// mapping, classification, retry) executes (ADR-021 §2.6). The zero value is
// "unset" — an internal resolution sentinel; a resolved job is always one of the
// two exported modes, defaulting to WorkerTrusted.
type TrustMode uint8

const (
	// trustUnset is the zero value: not configured (resolve to the default).
	trustUnset TrustMode = iota
	// WorkerTrusted (default) ships the policy to the worker, which maps its
	// output, self-classifies its faults, and retries technical faults
	// internally, reporting only a final verdict (ADR-021 §2.6).
	WorkerTrusted
	// EngineAuthoritative keeps the policy engine-side: the worker returns the raw
	// {code, body} and the engine maps / classifies / retries (ADR-021 §2.6).
	EngineAuthoritative
)

// trustModeNames is the mode→name table, keyed by the constant so it stays
// correct if the iota block is reordered. Keep it in sync with that block.
var trustModeNames = [...]string{
	trustUnset:          "unset",
	WorkerTrusted:       "workerTrusted",
	EngineAuthoritative: "engineAuthoritative",
}

// String returns the trust-mode name for logging.
func (m TrustMode) String() string {
	if int(m) >= len(trustModeNames) {
		return "unknown"
	}

	return trustModeNames[m]
}

// Resolve returns m when it is a configured mode, else fallback. It composes the
// two-level resolution — per-service over engine-wide over the WorkerTrusted
// default — while keeping the unset sentinel unexported:
//
//	perService.Resolve(engineWide.Resolve(WorkerTrusted))
func (m TrustMode) Resolve(fallback TrustMode) TrustMode {
	if m == trustUnset {
		return fallback
	}

	return m
}
