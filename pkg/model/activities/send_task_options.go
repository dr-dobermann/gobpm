package activities

import (
	"reflect"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
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

// Apply applies the send-task option to the provided configurator.
func (o SndTaskOption) Apply(cfg options.Configurator) error {
	if sc, ok := cfg.(*sndTaskConfig); ok {
		o(sc)

		return nil
	}

	return errs.New(
		errs.M("isn't sndTaskConfig"),
		errs.C(errorClass, errs.TypeCastingError),
		errs.D("cfg_type", reflect.TypeOf(cfg).String()))
}

// WithCorrelationKey declares the CorrelationKey the SendTask correlates its
// outgoing message on (ADR-016 v.1 §2.2): Send derives the key from the message
// payload and stamps it onto the published Envelope so a keyed consumer can
// correlate. A nil key is a no-op (name-match only).
func WithCorrelationKey(key *bpmncommon.CorrelationKey) SndTaskOption {
	return func(c *sndTaskConfig) {
		c.correlationKey = key
	}
}
