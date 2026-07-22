package events

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// ImplicitThrowEvent is a ThrowEvent thrown implicitly by an activity (BPMN
// §ImplicitThrowEvent), never reached by a token: it carries an EventDefinition
// and is emitted by the engine — here, a Multi-Instance behavior event thrown as
// instances complete (ADR-025 §2.8). Unlike IntermediateThrowEvent it declares
// no flow-node wiring and no trigger allow-list — the behavior event is the
// modeler's choice (typically a Signal, caught on the Multi-Instance activity's
// boundary). It reuses the shared throwEvent base, so its definition, per-instance
// cloning, and emit path come for free.
type ImplicitThrowEvent struct {
	throwEvent
}

// NewImplicitThrowEvent builds an implicit throw for def. A nil def is rejected.
func NewImplicitThrowEvent(
	name string,
	def flow.EventDefinition,
	baseOpts ...options.Option,
) (*ImplicitThrowEvent, error) {
	if def == nil {
		return nil, errs.New(
			errs.M("an event definition is required for the ImplicitThrowEvent"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	te, err := newThrowEvent(name, nil, []flow.EventDefinition{def}, baseOpts...)
	if err != nil {
		return nil, err
	}

	return &ImplicitThrowEvent{throwEvent: *te}, nil
}
