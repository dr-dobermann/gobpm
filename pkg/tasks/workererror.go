package tasks

import "github.com/dr-dobermann/gobpm/pkg/model/data"

// WorkerError is a WorkerFunc's rich, self-classifying error (ADR-021 §2.6): a
// WorkerTrusted worker returns it to declare its own outcome. Precedence is
// BpmnErrorCode → Status → technical (Cause). A plain (non-*WorkerError) error is
// an unclassified technical fault the worker runs through the fallback ErrorMapper.
type WorkerError struct {
	Cause         error      // the technical cause (a plain-technical verdict)
	Status        data.Value // non-nil → a Business Status verdict
	BpmnErrorCode string     // non-empty → a Business Error verdict
	Message       string     // optional Business Error diagnostic
}

// Error implements error, reporting the declared classification.
func (e *WorkerError) Error() string {
	switch {
	case e.BpmnErrorCode != "":
		if e.Message != "" {
			return "worker business error [" + e.BpmnErrorCode + "]: " + e.Message
		}

		return "worker business error [" + e.BpmnErrorCode + "]"

	case e.Status != nil:
		return "worker business status"

	case e.Cause != nil:
		return "worker technical fault: " + e.Cause.Error()

	default:
		return "worker error"
	}
}
