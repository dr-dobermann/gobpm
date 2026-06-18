package waiters

import (
	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/renv"
)

// errorClass is the error class for the waiters builder. Builder failures
// carry it so callers classify them like every sibling error in the event
// subsystem, instead of opaque fmt.Errorf strings.
const errorClass = "WAITERS_ERROR"

// CreateWaiter creates a new eventWaiter with given EventDefinition and
// EventProcessor. rt is the engine runtime the waiter uses to reach Clock /
// ExpressionEngine.
func CreateWaiter(
	eh eventproc.EventHub,
	ep eventproc.EventProcessor,
	eDef flow.EventDefinition,
	rt renv.EngineRuntime,
) (eventproc.EventWaiter, error) {
	if eh == nil {
		return nil, errs.New(
			errs.M("empty event hub isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if ep == nil {
		return nil, errs.New(
			errs.M("empty event processor isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if eDef == nil {
		return nil, errs.New(
			errs.M("empty event definition isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	var (
		w   eventproc.EventWaiter
		err error
	)

	// NOTE: timer and message are the only catchable triggers with a waiter
	// builder. Any future case added here (signal, conditional, …) MUST also
	// give its EventDefinition a CloneForInstance method (see
	// MessageEventDefinition / TimerEventDefinition), or concurrent instances
	// waiting on it will share one waiter and a single occurrence will resume
	// them all (the FIX-004 / SRD-017 per-instance-identity rule).
	switch eDef.Type() {
	case flow.TriggerTimer:
		w, err = NewTimeWaiter(eh, ep, eDef, "", rt)

	case flow.TriggerMessage:
		// CreateWaiter builds in-instance receivers — single-shot (the hub
		// removes them after one fire). The persistent instance-starter waiter
		// (SRD-015) is built on a dedicated path: CreatePersistentWaiter.
		w, err = NewMessageWaiter(eh, ep, eDef, "", rt, true)

	default:
		err = errs.New(
			errs.M("couldn't find builder for event definition of type %s",
				eDef.Type()),
			errs.C(errorClass, errs.ObjectNotFound),
			errs.D("event_definition_id", eDef.ID()),
			errs.D("event_definition_type", eDef.Type()))
	}

	return w, err
}

// CreatePersistentWaiter builds a persistent waiter for an event-triggered
// instance-starter (SRD-015): unlike CreateWaiter's single-shot in-instance
// receiver, the persistent waiter fires for every matching message and is
// retained by the EventHub until it is explicitly unregistered (ADR-006 v.1
// §2.5). Only message triggers can instantiate a process, so a non-message
// trigger is rejected.
func CreatePersistentWaiter(
	eh eventproc.EventHub,
	ep eventproc.EventProcessor,
	eDef flow.EventDefinition,
	rt renv.EngineRuntime,
) (eventproc.EventWaiter, error) {
	if eh == nil {
		return nil, errs.New(
			errs.M("empty event hub isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if ep == nil {
		return nil, errs.New(
			errs.M("empty event processor isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if eDef == nil {
		return nil, errs.New(
			errs.M("empty event definition isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if eDef.Type() != flow.TriggerMessage {
		return nil, errs.New(
			errs.M("only message triggers can back a persistent "+
				"instance-starter, got %s", eDef.Type()),
			errs.C(errorClass, errs.InvalidParameter),
			errs.D("event_definition_id", eDef.ID()),
			errs.D("event_definition_type", eDef.Type()))
	}

	return NewMessageWaiter(eh, ep, eDef, "", rt, false)
}
