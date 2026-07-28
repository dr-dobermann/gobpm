package instance

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// WakeParkedTrack routes a trigger from an engine-level holder into a RESIDENT
// instance's live loop (SRD-071 FR-3): the holder (e.g. the engine timer
// service) fired while the instance still holds its loop — it dehydrated only
// some of its tracks, or none yet — so the parked track resumes exactly as an
// in-hub waiter's ProcessEvent would, via evDeliver. The track is resolved by
// id from the lock-free snapshot; an unknown or already-moved track is a stale
// fire and a no-op (the wait moved on before the deadline). A DEHYDRATED
// instance never reaches here — the engine rebuilds it from its checkpoint
// instead (the wake fork, SRD-071 FR-4).
func (inst *Instance) WakeParkedTrack(
	trackID string, eDef flow.EventDefinition,
) error {
	if eDef == nil {
		return errs.New(
			errs.M("WakeParkedTrack: a nil trigger isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	snap := inst.tracksSnap.Load()
	if snap == nil {
		return nil
	}

	for _, t := range *snap {
		if t.ID() == trackID {
			inst.emit(trackEvent{kind: evDeliver, track: t, eDef: eDef})

			return nil
		}
	}

	return nil
}
