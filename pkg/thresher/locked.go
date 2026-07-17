package thresher

import (
	"context"

	"github.com/dr-dobermann/gobpm/internal/instance"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
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

// latestStartersLocked collects the instance-starters of the latest version of
// every registered key — the set Run wires onto the hub at startup (only the
// latest auto-starts; latest-supersedes).
func (t *Thresher) latestStartersLocked() []*instanceStarter {
	t.m.Lock()
	defer t.m.Unlock()

	var all []*instanceStarter
	for _, regs := range t.registrations {
		if n := len(regs); n > 0 {
			all = append(all, regs[n-1].starters...)
		}
	}

	return all
}

// reserveKeyLocked records the correlation key nsKey as seen, returning true if
// it was newly reserved or false if an instance already claimed it (a join, no
// duplicate). The check-and-record is atomic so two concurrent same-key starts
// cannot both create an instance.
func (t *Thresher) reserveKeyLocked(nsKey string) bool {
	t.m.Lock()
	defer t.m.Unlock()

	if _, seen := t.seenKeys[nsKey]; seen {
		return false
	}

	t.seenKeys[nsKey] = struct{}{}

	return true
}

// releaseKeyLocked drops a correlation-key reservation, letting a later message
// retry after a failed launch.
func (t *Thresher) releaseKeyLocked(nsKey string) {
	t.m.Lock()
	defer t.m.Unlock()

	delete(t.seenKeys, nsKey)
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

// trackInstanceLocked records a launched instance with its cancel func and a
// fresh read-only handle in the instances map, returning the handle. Shared by
// launchInstance and launchInstanceFromEvent.
func (t *Thresher) trackInstanceLocked(
	inst *instance.Instance,
	cancel context.CancelFunc,
) *InstanceHandle {
	h := &InstanceHandle{inst: inst}

	t.m.Lock()
	defer t.m.Unlock()

	t.instances[inst.ID()] = instanceReg{
		stop:   cancel,
		inst:   inst,
		handle: h,
	}

	return h
}
