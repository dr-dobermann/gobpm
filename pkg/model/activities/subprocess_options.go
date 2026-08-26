package activities

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/lanes"
)

// subProcessConfig collects the SubProcess-specific construction options.
type subProcessConfig struct {
	adHoc *adHocSpec

	// tx is set by WithTransaction: the characteristics that make the
	// Sub-Process a Transaction (ADR-028 §2.1), nil for every other variant.
	tx *TransactionCharacteristics

	// laneSets are carried through to the SubProcess; lanes are model-only and
	// never reach execution (SRD-076).
	laneSets []*lanes.LaneSet

	triggered bool
}

// AddLaneSet implements lanes.LaneSetAdder — a Sub-Process is one of the two
// FlowElementsContainers BPMN hangs laneSets off.
// A nil set cannot arrive here: lanes.WithLaneSets refuses one before calling,
// and this config is unexported so nothing else can. A guard would be
// unreachable code.
func (cfg *subProcessConfig) AddLaneSet(ls *lanes.LaneSet) error {
	cfg.laneSets = append(cfg.laneSets, ls)

	return nil
}

// SubProcessOption is a SubProcess-specific construction option. NewSubProcess
// separates these from the embedded activity's options and applies them to the
// SubProcess itself.
type SubProcessOption func(*subProcessConfig) error

// Option marks SubProcessOption as an options.Option; NewSubProcess applies it
// by calling the func directly.
func (SubProcessOption) Option() {}

// WithTriggeredByEvent marks the SubProcess as an Event Sub-Process (BPMN
// §13.5.4, ADR-023 v.2 §2.10): a handler armed while its enclosing scope is
// open, entered only when its single triggered Start Event fires — not by a
// sequence flow. Its inner graph must then have exactly one interrupting
// triggered start (Message/Timer/Signal/Error/Conditional) instead of the
// embedded Sub-Process's None-start / flow-less entry (SRD-052).
func WithTriggeredByEvent() SubProcessOption {
	return SubProcessOption(func(cfg *subProcessConfig) error {
		cfg.triggered = true

		return nil
	})
}

// WithTransaction makes the SubProcess a Transaction Sub-Process (BPMN §10.7,
// ADR-028 §2.1): a plain embedded Sub-Process in every respect except that
// reaching a Cancel End Event inside it triggers an ACID-like abort —
// compensate its completed inner activities, terminate the running ones, and
// leave through its Cancel boundary. The characteristics permit Cancel (End +
// boundary), name the scope a cancel aborts, and carry the abort method and
// coordination protocol the document stated (ADR-028 §2.7); with no options
// the method is compensate and no protocol is stated. Mutually exclusive with
// WithTriggeredByEvent (a handler is not a transaction).
func WithTransaction(opts ...TransactionOption) SubProcessOption {
	return SubProcessOption(func(cfg *subProcessConfig) error {
		tc := &TransactionCharacteristics{method: TransactionCompensate}

		for _, o := range opts {
			if o == nil {
				return errs.New(
					errs.M("WithTransaction: a nil TransactionOption isn't allowed"),
					errs.C(errorClass, errs.InvalidParameter))
			}

			if err := o(tc); err != nil {
				return err
			}
		}

		cfg.tx = tc

		return nil
	})
}
