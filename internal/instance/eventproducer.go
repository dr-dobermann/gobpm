package instance

import (
	"context"
	"errors"
	"strings"

	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// -------------------- exec.EventProducer interface ---------------------------

// RegisterEvent register tracks awaited for the event.
// Once event is fired, then track's EventProcessor called.
func (inst *Instance) RegisterEvent(
	proc eventproc.EventProcessor,
	eDef flow.EventDefinition,
) error {
	// Validate the arguments BEFORE the state-specific branch below builds a
	// diagnostic from proc.ID() — a nil processor must not panic the terminal
	// path (FIX-010).
	if proc == nil {
		return errs.New(
			errs.M("empty EventProcessor"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if eDef == nil {
		return errs.New(
			errs.M("empty EventDefinition"),
			errs.C(errorClass, errs.EmptyNotAllowed, errs.InvalidParameter))
	}

	// Event registration is legitimate while the instance is being built
	// (Created — start-event nodes register here) or running (Active — boundary
	// / intermediate catch events); it is refused only on a terminal instance
	// that can no longer act on a fired event (FIX-002 RC1).
	is := inst.State()
	if is != Created && is != Active {
		return errs.New(
			errs.M("instance is terminal, can't register events (state: %s)",
				is),
			errs.C(errorClass, errs.InvalidState),
			errs.D("requester_id", proc.ID()))
	}

	if inst.parentEventProducer == nil {
		return errs.New(
			errs.M("no registered EventProducer"),
			errs.C(errorClass, errs.InvalidObject))
	}

	return inst.parentEventProducer.RegisterEvent(
		proc, eDef)
}

// UnregisterEvent removes the eDefID-to-proc subscription, mirroring
// RegisterEvent: it validates its arguments and delegates to the parent
// EventProducer.
//
// It is idempotent: if the parent reports the waiter or the processor is
// already gone (ObjectNotFound), the desired end state — proc no longer
// subscribed to eDefID — is already reached, so it returns nil. This keeps
// the fired-event flow working, where the waiter self-removes before the
// track unregisters (track.go unregisterEvent). Resolving who OWNS the
// waiter's lifecycle (the hub vs the waiter) is ADR-006's concern; this is
// the interim seam.
func (inst *Instance) UnregisterEvent(
	proc eventproc.EventProcessor,
	eDefID string,
) error {
	if proc == nil {
		return errs.New(
			errs.M("empty EventProcessor"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if strings.TrimSpace(eDefID) == "" {
		return errs.New(
			errs.M("empty event definition id"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if inst.parentEventProducer == nil {
		return errs.New(
			errs.M("no registered EventProducer"),
			errs.C(errorClass, errs.InvalidObject))
	}

	err := inst.parentEventProducer.UnregisterEvent(proc, eDefID)

	var ae *errs.ApplicationError
	if errors.As(err, &ae) && ae.HasClass(errs.ObjectNotFound) {
		return nil
	}

	return err
}

// PropagateEvent sends a fired throw event's eventDefinition
// up to chain of EventProducers
func (inst *Instance) PropagateEvent(
	ctx context.Context,
	eDef flow.EventDefinition,
) error {
	st := inst.State()
	if st != Active {
		return errs.New(
			errs.M("instance isn't active"),
			errs.C(errorClass, errs.InvalidState),
			errs.D("current_state", st.String()),
			errs.D("instance_id", inst.ID()))
	}

	if err := inst.parentEventProducer.PropagateEvent(ctx, eDef); err != nil {
		return errs.New(
			errs.M("event propagation failed"),
			errs.C(errorClass, errs.OperationFailed),
			errs.D("event_definition_id", eDef.ID()),
			errs.D("event_definition_type", string(eDef.Type())),
			errs.E(err))
	}

	return nil
}

// ------------------ instance identity & services -----------------------------

// InstanceID returns ID of the Instance.
func (inst *Instance) InstanceID() string {
	return inst.ID()
}

// EventProducer returns the EventProducer of the runtime.
func (inst *Instance) EventProducer() eventproc.EventProducer {
	return inst
}

// =============================================================================
// Interfaces check
var (
	_ eventproc.EventProducer   = (*Instance)(nil)
	_ scope.RuntimeVarsSupplier = (*Instance)(nil)
)
