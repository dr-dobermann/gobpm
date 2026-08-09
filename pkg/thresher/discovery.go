package thresher

import (
	"github.com/dr-dobermann/gobpm/internal/instance"
	"github.com/dr-dobermann/gobpm/pkg/errs"
)

// InstanceKind is the root/child axis of an InstanceQuery (SRD-084).
type InstanceKind uint8

const (
	// KindAny matches roots and children alike (the zero value).
	KindAny InstanceKind = iota
	// KindRoots matches only ROOT instances — the ones a host lists as
	// "processes". Call Activity children are reachable through their
	// parent (InstanceHandle.ParentID).
	KindRoots
	// KindChildren matches only Call Activity children — instances
	// launched by a caller's InvokeProcess. (Multi-Instance iterations
	// are scopes inside ONE instance and never appear here.)
	KindChildren

	kindEnd // the validation bound, not a value
)

// InstanceStage is the lifecycle axis of an InstanceQuery (SRD-084) —
// deliberately coarser than InstanceState: running vs settled is what
// discovery composes on.
type InstanceStage uint8

const (
	// StageAny matches every lifecycle state (the zero value).
	StageAny InstanceStage = iota
	// StageRunning matches the non-terminal instances
	// (Created/Active/Terminating).
	StageRunning
	// StageSettled matches the terminal instances
	// (Completed/Terminated) — the ones Forget can release.
	StageSettled

	stageEnd // the validation bound, not a value
)

// InstanceQuery selects tracked instances (SRD-084). The predicates
// AND together, and the zero value selects every tracked instance.
// ProcessID and ParentID are exact matches; "" means any. A
// contradictory combination (KindRoots with a non-empty ParentID) is a
// well-formed empty intersection, not an error.
type InstanceQuery struct {
	ProcessID string
	ParentID  string
	Kind      InstanceKind
	Stage     InstanceStage
}

// instanceTerminal reports whether an instance lifecycle state is terminal.
func instanceTerminal(s instance.State) bool {
	return s == instance.Completed || s == instance.Terminated
}

// Instances returns the ids of tracked instances matching the query
// (SRD-084): the axes AND together and InstanceQuery{} lists
// everything. An out-of-range Kind or Stage refuses — the retired
// filter enum silently returned EVERYTHING for an unknown value, and
// a widened result for invalid input is exactly the defect class the
// validation rule forbids. The host reads each id's state/tokens/data
// via Instance(id). Snapshot-consistent under the engine lock; order
// is unspecified.
func (t *Thresher) Instances(q InstanceQuery) ([]string, error) {
	if q.Kind >= kindEnd {
		return nil, errs.New(
			errs.M("Instances: unknown Kind %d", q.Kind),
			errs.C(errorClass, errs.InvalidParameter))
	}

	if q.Stage >= stageEnd {
		return nil, errs.New(
			errs.M("Instances: unknown Stage %d", q.Stage),
			errs.C(errorClass, errs.InvalidParameter))
	}

	t.m.Lock()
	defer t.m.Unlock()

	out := make([]string, 0, len(t.instances))

	for id, reg := range t.instances {
		if !q.matches(reg.inst) {
			continue
		}

		out = append(out, id)
	}

	return out, nil
}

// matches applies the query's ANDed predicates to one instance. Runs
// under the engine lock.
func (q InstanceQuery) matches(inst *instance.Instance) bool {
	if q.Kind == KindRoots && inst.ParentID() != "" {
		return false
	}

	if q.Kind == KindChildren && inst.ParentID() == "" {
		return false
	}

	terminal := instanceTerminal(inst.State())
	if q.Stage == StageRunning && terminal {
		return false
	}

	if q.Stage == StageSettled && !terminal {
		return false
	}

	if q.ProcessID != "" && inst.ProcessID() != q.ProcessID {
		return false
	}

	if q.ParentID != "" && inst.ParentID() != q.ParentID {
		return false
	}

	return true
}

// Forget releases the listed terminal instances from the engine's tracking
// (SRD-019), so a long-running engine doesn't accumulate finished instances.
// All-or-nothing: every id is validated first (known AND terminal); on any
// unknown or still-live id none are removed and an error naming it is returned.
// Forget over an InstanceQuery{Stage: StageSettled} listing sweeps all
// finished instances.
func (t *Thresher) Forget(ids ...string) error {
	stops, err := t.forgetLocked(ids)
	if err != nil {
		return err
	}

	// Release each instance's context OUTSIDE t.m. Every launch path derives
	// the instance's context from the engine's and retains the cancel here, so
	// until it is called the child stays attached to the engine context's
	// children — for the engine's whole lifetime. Forget is the reaping path;
	// reaping the registration and leaving the context behind is exactly the
	// accumulation this method exists to prevent (FIX-036 §8.2).
	for _, stop := range stops {
		stop()
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
