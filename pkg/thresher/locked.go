package thresher

import (
	"context"
	"sort"

	"github.com/dr-dobermann/gobpm/internal/instance"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// This file holds every t.m-confined registry operation. Each helper acquires
// t.m, touches ONLY the four registry maps (registrations, nextVersion,
// instances, seenKeys), and returns plain data — it never takes a callback or
// runs an EventHub / launchInstance call. Callers do the hub/launch work AFTER
// the helper has returned (and the lock released), so it is impossible by
// construction to hold t.m across an engine-subsystem call — the FIX-002 RC2
// deadlock class the audit (§2.6) flagged.

// appendVersionLocked records a new version of s under its process key: it mints
// the next monotonic version, builds the registration, and appends it. It
// returns the new registration and the previous latest (nil if this is the
// first version), which the caller uses to drive latest-supersedes hub work.
func (t *Thresher) appendVersionLocked(
	s *snapshot.Snapshot,
	starters []*instanceStarter,
	manual bool,
) (reg, prevLatest *ProcessRegistration) {
	t.m.Lock()
	defer t.m.Unlock()

	prev := t.registrations[s.ProcessID]
	if len(prev) > 0 {
		prevLatest = prev[len(prev)-1]
	}

	// Version numbers come from a per-key monotonic counter, never the slice
	// length: removing a non-latest version must not make the next registration
	// reuse a still-live version number. The counter resets only when the key is
	// fully unregistered (removeKeyLocked / full removeVersionLocked).
	t.nextVersion[s.ProcessID]++
	// The snapshot carries its registered version (SRD-070 FR-1) — every
	// instance clone inherits it, so checkpoints can pin what they ran.
	s.Version = t.nextVersion[s.ProcessID]
	reg = &ProcessRegistration{
		key:      s.ProcessID,
		version:  t.nextVersion[s.ProcessID],
		id:       foundation.GenerateID(),
		snapshot: s,
		starters: starters,
		manual:   manual,
	}
	t.registrations[s.ProcessID] = append(prev, reg)

	return reg, prevLatest
}

// removeVersionLocked drops the single registration reg from its key. It returns
// whether reg was found, whether it was the live latest, and the now-newest
// remaining version's starters to promote (nil unless the latest was removed and
// another version remains). Fully removing the last version forgets the version
// counter so a later re-registration of the key restarts at v1.
func (t *Thresher) removeVersionLocked(
	reg *ProcessRegistration,
) (found, wasLatest bool, promote []*instanceStarter) {
	t.m.Lock()
	defer t.m.Unlock()

	regs := t.registrations[reg.key]
	idx := -1
	for i, r := range regs {
		if r == reg {
			idx = i

			break
		}
	}
	if idx < 0 {
		return false, false, nil
	}

	wasLatest = idx == len(regs)-1
	regs = append(regs[:idx], regs[idx+1:]...)
	if len(regs) == 0 {
		delete(t.registrations, reg.key)
		delete(t.nextVersion, reg.key)

		return true, wasLatest, nil
	}

	t.registrations[reg.key] = regs
	if wasLatest {
		promote = regs[len(regs)-1].starters
	}

	return true, wasLatest, promote
}

// removeKeyLocked drops every version of key and forgets its version counter. It
// returns the latest version's starters (the only live ones — latest-supersedes)
// for the caller to tear down from the hub, and whether the key existed.
func (t *Thresher) removeKeyLocked(
	key string,
) (liveStarters []*instanceStarter, existed bool) {
	t.m.Lock()
	defer t.m.Unlock()

	regs := t.registrations[key]
	if len(regs) == 0 {
		return nil, false
	}

	liveStarters = regs[len(regs)-1].starters
	delete(t.registrations, key)
	delete(t.nextVersion, key)

	return liveStarters, true
}

// claimLatestRegistrationsLocked claims the hub wiring of the latest version of
// every registered key — the set Run wires at startup (only the latest
// auto-starts; latest-supersedes) — and returns the registrations it claimed,
// so a caller that fails can give every one of them back.
//
// The claim is what makes the two wiring paths idempotent against each other
// (FIX-036 §1.8). Run publishes Started before it sweeps, so a RegisterProcess
// that lands in between sees a started engine and wires its own starters, while
// the sweep still finds that version in the registry and would wire them a
// second time. Whichever arrives first claims; the other skips that key.
func (t *Thresher) claimLatestRegistrationsLocked() []*ProcessRegistration {
	t.m.Lock()
	defer t.m.Unlock()

	var claimed []*ProcessRegistration

	for _, regs := range t.registrations {
		n := len(regs)
		if n == 0 || regs[n-1].wired {
			continue
		}

		regs[n-1].wired = true

		claimed = append(claimed, regs[n-1])
	}

	return claimed
}

// claimWiringLocked claims the hub wiring of reg's starters, returning false if
// they are already claimed — the RegisterProcess half of the same handshake.
func (t *Thresher) claimWiringLocked(reg *ProcessRegistration) bool {
	t.m.Lock()
	defer t.m.Unlock()

	if reg.wired {
		return false
	}

	reg.wired = true

	return true
}

// releaseWiringLocked records that reg's starters have left the hub, so a later
// promotion — or a retry after a failed registration — can wire them again.
func (t *Thresher) releaseWiringLocked(reg *ProcessRegistration) {
	t.m.Lock()
	defer t.m.Unlock()

	reg.wired = false
}

// setLatestWiredLocked records whether key's latest registration owns the live
// starter set. It is how promote-on-removal claims the wiring for the version
// it just promoted, whose registration the remover does not hold.
func (t *Thresher) setLatestWiredLocked(key string, wired bool) {
	t.m.Lock()
	defer t.m.Unlock()

	if regs := t.registrations[key]; len(regs) > 0 {
		regs[len(regs)-1].wired = wired
	}
}

// keyInFlight is the reservation's value while its instance is being launched:
// the key is claimed (a concurrent same-key start must not create a second
// instance) but no instance owns it yet. bindKeyLocked replaces it with the id.
const keyInFlight = ""

// reserveKeyLocked claims the correlation key nsKey for a NEW instance,
// returning false when the key belongs to a conversation that is still going —
// a live instance (join, no duplicate) or a start already in flight.
//
// A reservation whose instance has finished is NOT such a conversation: it is
// taken over, so a later message carrying the same business key starts a new
// process instead of joining an instance that no longer exists (FIX-036 §1.2).
// The check-and-record is atomic, so two concurrent same-key starts still
// cannot both create an instance.
func (t *Thresher) reserveKeyLocked(nsKey string) bool {
	t.m.Lock()
	defer t.m.Unlock()

	if owner, seen := t.seenKeys[nsKey]; seen && t.keyOwnerLiveLocked(owner) {
		return false
	}

	t.seenKeys[nsKey] = keyInFlight

	return true
}

// keyOwnerLiveLocked reports whether a reservation still names a conversation
// nothing may duplicate: a start in flight, or a tracked instance that has not
// reached a terminal state. An id the registry no longer knows — forgotten, or
// lost with the engine that ran it — is not live. Caller holds m.
func (t *Thresher) keyOwnerLiveLocked(owner string) bool {
	if owner == keyInFlight {
		return true
	}

	reg, tracked := t.instances[owner]
	if !tracked {
		return false
	}

	return !instanceTerminal(reg.inst.State())
}

// bindKeyLocked names the instance that owns a reservation, once its launch has
// succeeded (FIX-036 §1.2).
func (t *Thresher) bindKeyLocked(nsKey, instanceID string) {
	t.m.Lock()
	defer t.m.Unlock()

	t.seenKeys[nsKey] = instanceID
}

// releaseKeyLocked drops a correlation-key reservation, letting a later message
// retry after a failed launch.
func (t *Thresher) releaseKeyLocked(nsKey string) {
	t.m.Lock()
	defer t.m.Unlock()

	delete(t.seenKeys, nsKey)
}

// rebindKeysLocked re-establishes the correlation reservations of an instance
// the engine has just rebuilt from its checkpoint — a cold restart recovery or
// a wake (FIX-036 §1.2). seenKeys is in-memory bookkeeping, so without this a
// live conversation comes back unreserved and the next message carrying its key
// starts a DUPLICATE instance beside it. The keys are the conversation values
// the checkpoint carries (ADR-033 §2.1's ConvKeys), which are the same values
// the instance-starter reserved when it first created the conversation.
func (t *Thresher) rebindKeysLocked(
	processID, instanceID string,
	convKeys map[string]string,
) {
	if len(convKeys) == 0 {
		return
	}

	t.m.Lock()
	defer t.m.Unlock()

	for _, v := range convKeys {
		if v == "" {
			continue
		}

		t.seenKeys[nsKeyFor(processID, v)] = instanceID
	}
}

// releaseKeysOfLocked drops every reservation owned by instanceID — the
// forgetting half of §1.2, so the map shrinks with the instances it tracks
// rather than growing for the engine's lifetime. Caller holds m.
func (t *Thresher) releaseKeysOfLocked(instanceID string) {
	for nsKey, owner := range t.seenKeys {
		if owner == instanceID {
			delete(t.seenKeys, nsKey)
		}
	}
}

// pendingInstancesLocked returns the tracked instances whose ids are NOT in
// settled — the work one Shutdown drain pass still has to await. A fresh call
// picks up anything born since the previous pass (FIX-036 §1.7).
func (t *Thresher) pendingInstancesLocked(
	settled map[string]struct{},
) []instanceReg {
	t.m.Lock()
	defer t.m.Unlock()

	pending := make([]instanceReg, 0, len(t.instances))

	for id, r := range t.instances {
		if _, done := settled[id]; !done {
			pending = append(pending, r)
		}
	}

	return pending
}

// unsettledIDsLocked names the tracked instances that never settled, sorted so
// a timeout error reads the same way twice.
func (t *Thresher) unsettledIDsLocked(settled map[string]struct{}) []string {
	t.m.Lock()
	defer t.m.Unlock()

	ids := make([]string, 0, len(t.instances))

	for id := range t.instances {
		if _, done := settled[id]; !done {
			ids = append(ids, id)
		}
	}

	sort.Strings(ids)

	return ids
}

// forgetLocked validates and removes the listed terminal instances, returning
// the retained context-cancels of the ones it removed so the CALLER can invoke
// them outside t.m — the same shape every other helper here follows.
//
// All-or-nothing: every id is validated first (known AND terminal); on any
// unknown or still-live id nothing is removed and an error naming it comes back.
func (t *Thresher) forgetLocked(
	ids []string,
) ([]context.CancelFunc, error) {
	t.m.Lock()
	defer t.m.Unlock()

	for _, id := range ids {
		reg, ok := t.instances[id]
		if !ok {
			return nil, errs.New(
				errs.M("unknown instance %q", id),
				errs.C(errorClass, errs.ObjectNotFound))
		}

		if st := reg.inst.State(); !instanceTerminal(st) {
			return nil, errs.New(
				errs.M("instance %q is still live (%s); cancel it before forgetting",
					id, st.String()),
				errs.C(errorClass, errs.InvalidState))
		}
	}

	stops := make([]context.CancelFunc, 0, len(ids))

	for _, id := range ids {
		if reg := t.instances[id]; reg.stop != nil {
			stops = append(stops, reg.stop)
		}

		delete(t.instances, id)

		// Forget everything the engine holds for the instance, not just its
		// registration (FIX-036 §1.2/§1.3): its terminal signal and any
		// correlation reservation it owns. Both are safe here precisely
		// because the loop above proved the instance terminal — nothing can
		// rebuild it and wait on a channel we dropped.
		delete(t.settled, id)
		t.releaseKeysOfLocked(id)
	}

	return stops, nil
}

// latestSnapshotLocked returns the snapshot of the latest registered version of
// key, or nil if no version is registered. The slice is kept in ascending
// version order, so the last element is the latest.
func (t *Thresher) latestSnapshotLocked(key string) *snapshot.Snapshot {
	t.m.Lock()
	defer t.m.Unlock()

	if regs := t.registrations[key]; len(regs) > 0 {
		return regs[len(regs)-1].snapshot
	}

	return nil
}

// resolveCallLocked resolves a Call Activity binding to a snapshot AND its
// resolved 1-based version (SRD-050): version 0 binds latest-at-launch (the last
// element, ascending order), else the pinned version (scanned by NUMBER, gap-safe
// like snapshotForVersionLocked). ok is false when no matching registration
// exists — the caller turns that into a classified call-resolution error. The
// resolved version is returned because a latest-at-launch call must record which
// concrete version it actually bound (the KindCall audit point, ADR-023 §6).
func (t *Thresher) resolveCallLocked(
	key string,
	version int,
) (s *snapshot.Snapshot, resolved int, ok bool) {
	t.m.Lock()
	defer t.m.Unlock()

	regs := t.registrations[key]
	if len(regs) == 0 {
		return nil, 0, false
	}

	if version == 0 {
		last := regs[len(regs)-1]

		return last.snapshot, last.version, true
	}

	for _, r := range regs {
		if r.version == version {
			return r.snapshot, r.version, true
		}
	}

	return nil, 0, false
}

// snapshotForVersionLocked returns the snapshot of the specific version of key,
// or nil if no such version is registered. It scans by version NUMBER (not slice
// position) since removals can leave gaps (v1, v3, …).
func (t *Thresher) snapshotForVersionLocked(
	key string,
	version int,
) *snapshot.Snapshot {
	t.m.Lock()
	defer t.m.Unlock()

	for _, r := range t.registrations[key] {
		if r.version == version {
			return r.snapshot
		}
	}

	return nil
}

// handleForLocked returns the handle for an instance id, minting one (with its
// per-ID terminal signal) on first sight. Caller holds t.m.
func (t *Thresher) handleForLocked(
	instanceID string, settled chan struct{},
) *InstanceHandle {
	if reg, ok := t.instances[instanceID]; ok && reg.handle != nil {
		return reg.handle
	}

	t.settled[instanceID] = settled

	return &InstanceHandle{settled: settled, th: t}
}

// settledFor returns the per-instance-ID terminal signal, creating it if this
// is the first time the engine touches the id. The SAME channel is handed to
// every rebuild, so a host waiting for completion is not woken by a mere
// dehydration (SRD-071).
func (t *Thresher) settledFor(instanceID string) chan struct{} {
	t.m.Lock()
	defer t.m.Unlock()

	if ch, ok := t.settled[instanceID]; ok {
		return ch
	}

	ch := make(chan struct{})
	t.settled[instanceID] = ch

	return ch
}

// trackInstanceLocked records a launched instance with its cancel and its
// read-only handle, returning the handle AND the cancel it displaced.
//
// The displaced cancel belongs to the PREVIOUS incarnation's context, and the
// caller must run it once the lock is released (FIX-037 §1.4). Dropping it
// leaves that child attached to the engine context's children for the engine's
// whole lifetime, and a dehydrating instance replaces its registration on every
// wake — so the leak is per cycle, not per instance. It is returned rather than
// canceled here because canceling under t.m is the shape this file exists to
// forbid. A first registration displaces nothing and returns nil.
func (t *Thresher) trackInstanceLocked(
	inst *instance.Instance,
	cancel context.CancelFunc,
	settled chan struct{},
) (*InstanceHandle, context.CancelFunc) {
	t.m.Lock()
	defer t.m.Unlock()

	// A REBUILT instance keeps the handle callers already hold (SRD-071): the
	// instance's identity outlives its object, so the handle is re-pointed at
	// the new one rather than replaced — otherwise every reference taken before
	// a dehydration would answer for a dead object forever.
	h := t.handleForLocked(inst.ID(), settled)
	h.adopt(inst)

	displaced := t.instances[inst.ID()].stop

	t.instances[inst.ID()] = instanceReg{
		stop:   cancel,
		inst:   inst,
		handle: h,
	}

	return h, displaced
}

// stopDisplaced runs the cancel trackInstanceLocked displaced, if any. Always
// call it OUTSIDE t.m.
func stopDisplaced(displaced context.CancelFunc) {
	if displaced != nil {
		displaced()
	}
}
