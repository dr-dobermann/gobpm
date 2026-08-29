package instance

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// WakeParkedTrack routes a trigger from an engine-level holder into a RESIDENT
// iteration's live loop (SRD-071 FR-3): the holder (e.g. the engine timer
// service) fired while the instance still holds its loop — it dehydrated only
// some of its tracks, or none yet — so the parked track resumes exactly as an
// in-hub waiter's ProcessEvent would, via evDeliver. The track is resolved by
// id from the lock-free snapshot; an unknown or already-moved track is a stale
// fire and a no-op (the wait moved on before the deadline). A DEHYDRATED
// iteration never reaches here — the engine rebuilds it from its checkpoint
// instead (the wake fork, SRD-071 FR-4).
func (inst *Instance) WakeParkedTrack(
	trackID string, eDef flow.EventDefinition,
) (delivered bool, err error) {
	if eDef == nil {
		return false, errs.New(
			errs.M("WakeParkedTrack: a nil trigger isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	// A MESSAGE keeps the track-less shape the loop's correlation gate keys on
	// (dispatchToParked gates exactly when ev.track == nil, resolving the target
	// through msgIdx): routing it with a track would silently SKIP correlation
	// and deliver a foreign conversation's message. Signal/timer carry their
	// track, as they always have.
	if eDef.Type() == flow.TriggerMessage {
		return inst.offer(trackEvent{kind: evDeliver, eDef: eDef}), nil
	}

	snap := inst.tracksSnap.Load()
	if snap == nil {
		return true, nil // nothing spawned — a stale fire, benignly dropped
	}

	for _, t := range *snap {
		if t.ID() == trackID {
			return inst.offer(
				trackEvent{kind: evDeliver, track: t, eDef: eDef}), nil
		}
	}

	return true, nil // the wait moved on — a stale fire, benignly dropped
}

// offer hands an event to the loop, reporting whether the loop TOOK it. The
// send blocks until the loop reads it or the loop is gone, and the loop reads
// only from inside its event body — which applies the event before it can
// evaluate dehydration. So a true return means the trigger is guaranteed to be
// applied to a live loop, and a false return means the instance released its
// goroutines underneath us and must be woken from its checkpoint instead
// (SRD-071 §4.6 / NFR-1: a trigger racing the cut is never lost).
func (inst *Instance) offer(ev trackEvent) bool {
	select {
	case inst.events <- ev:
		return true

	case <-inst.loopDone:
		return false
	}
}
