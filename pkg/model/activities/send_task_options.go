package activities

import (
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
)

// sndTaskConfig collects the SendTask-specific options (those that don't belong
// to the embedded task) applied at NewSendTask.
type sndTaskConfig struct {
	correlationKey *bpmncommon.CorrelationKey
}

// Validate implements options.Configurator; sndTaskConfig has no constraints.
func (*sndTaskConfig) Validate() error {
	return nil
}

// SndTaskOption is a SendTask-specific construction option (e.g.
// WithCorrelationKey). NewSendTask separates these from the embedded task's
// options and applies them to the SendTask itself.
type SndTaskOption func(*sndTaskConfig)

// Option marks SndTaskOption as an options.Option; NewSendTask applies it by
// calling the func directly.
func (SndTaskOption) Option() {}

// WithCorrelationKey declares the CorrelationKey the SendTask correlates its
// outgoing message on (ADR-016 v.1 §2.2): Send derives the key from the message
// payload and stamps it onto the published Envelope so a keyed consumer can
// correlate. A nil key is a no-op (name-match only).
func WithCorrelationKey(key *bpmncommon.CorrelationKey) SndTaskOption {
	return func(c *sndTaskConfig) {
		c.correlationKey = key
	}
}
