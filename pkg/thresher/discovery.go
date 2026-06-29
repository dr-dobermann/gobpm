package thresher

import (
	"github.com/dr-dobermann/gobpm/internal/instance"
	"github.com/dr-dobermann/gobpm/pkg/errs"
)

// InstanceFilter selects which tracked instances Instances returns (SRD-019).
type InstanceFilter uint8

const (
	// InstancesAll returns every tracked instance.
	InstancesAll InstanceFilter = iota
	// InstancesRunning returns the non-terminal instances (Created/Active/
	// Terminating).
	InstancesRunning
	// InstancesCompleted returns the terminal instances (Completed/Terminated) —
	// the ones Forget can release.
	InstancesCompleted
)

// instanceTerminal reports whether an instance lifecycle state is terminal.
func instanceTerminal(s instance.State) bool {
	return s == instance.Completed || s == instance.Terminated
}

// Instances returns the ids of tracked instances matching filter (SRD-019).
// The host reads each one's state/tokens/data via Instance(id). Snapshot-
// consistent under the engine lock; order is unspecified.
func (t *Thresher) Instances(filter InstanceFilter) []string {
	t.m.Lock()
	defer t.m.Unlock()

	out := make([]string, 0, len(t.instances))

	for id, reg := range t.instances {
		terminal := instanceTerminal(reg.inst.State())

		switch filter {
		case InstancesRunning:
			if terminal {
				continue
			}

		case InstancesCompleted:
			if !terminal {
				continue
			}
		}

		out = append(out, id)
	}

	return out
}

// Forget releases the listed terminal instances from the engine's tracking
// (SRD-019), so a long-running engine doesn't accumulate finished instances.
// All-or-nothing: every id is validated first (known AND terminal); on any
// unknown or still-live id none are removed and an error naming it is returned.
// Forget(Instances(InstancesCompleted)...) sweeps all finished instances.
func (t *Thresher) Forget(ids ...string) error {
	t.m.Lock()
	defer t.m.Unlock()

	for _, id := range ids {
		reg, ok := t.instances[id]
		if !ok {
			return errs.New(
				errs.M("unknown instance %q", id),
				errs.C(errorClass, errs.ObjectNotFound))
		}

		if st := reg.inst.State(); !instanceTerminal(st) {
			return errs.New(
				errs.M("instance %q is still live (%s); cancel it before forgetting",
					id, st.String()),
				errs.C(errorClass, errs.InvalidState))
		}
	}

	for _, id := range ids {
		delete(t.instances, id)
	}

	return nil
}

// Registrations returns the registered versions of a process key, ascending by
// version (an empty slice for an unknown key). Each element is a live handle —
// read its `Version()` / `ID()`, or pass it straight to `StartProcess` /
// `UnregisterProcess`. Because removing a non-latest version may leave gaps
// (v1, v3, …), this is how a caller discovers which versions exist before
// addressing one by `StartVersion`. Snapshot-consistent under the engine lock.
func (t *Thresher) Registrations(key string) []*ProcessRegistration {
	t.m.Lock()
	defer t.m.Unlock()

	regs := t.registrations[key]
	out := make([]*ProcessRegistration, len(regs))
	copy(out, regs)

	return out
}

// StarterInfo describes one event-start registration (SRD-019): a process
// awaiting an event to instantiate — there is no instance yet, so it cannot
// appear under Instances. A manual-start process registers no starter, so every
// listed starter is auto-start.
type StarterInfo struct {
	ProcessID string // the process a matching event instantiates
	StartNode string // the start node fired on a match
	Trigger   string // the message the starter waits on
}

// Starters lists the registered event-start registrations (SRD-019).
// Snapshot-consistent under the engine lock; order is unspecified.
func (t *Thresher) Starters() []StarterInfo {
	t.m.Lock()
	defer t.m.Unlock()

	out := make([]StarterInfo, 0, len(t.registrations))

	// Only the latest version of a key has live starters (latest-supersedes), so
	// the live starter set is the latest registration's per key.
	for key, regs := range t.registrations {
		n := len(regs)
		if n == 0 {
			continue
		}

		for _, s := range regs[n-1].starters {
			out = append(out, StarterInfo{
				ProcessID: key,
				StartNode: s.startNode.Name(),
				Trigger:   triggerName(s.eDef),
			})
		}
	}

	return out
}
