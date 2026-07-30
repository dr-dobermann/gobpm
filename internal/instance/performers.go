package instance

import "sync"

// performers records who actually completed each of the instance's human tasks —
// BPMN's actualOwner outliving the task it belonged to (ADR-020 v.2 §2.4.2).
//
// It is engine-published, so it is served through the reserved read-only RUNTIME
// subtree rather than committed into the data plane: a process must be able to READ
// who performed a task, and must not be able to overwrite the record or collide with
// it by naming a variable the same way.
//
// Keyed by node name (falling back to node id when the name is empty), because that
// is the handle a modeler writing an expression has. A looped or multi-instance task
// completes more than once and each pass overwrites its entry, so the record names
// the LAST completer; the per-iteration trail belongs to the observer stream.
//
// Guarded by its own mutex: entries are written on the instance-loop goroutine at
// completion and read during expression evaluation on track goroutines.
type performers struct {
	byNode map[string]string
	m      sync.Mutex
}

// newPerformers builds an empty register.
func newPerformers() *performers {
	return &performers{byNode: map[string]string{}}
}

// record notes that userID completed the task at node. Later completions of the same
// node overwrite the entry.
func (p *performers) record(node, userID string) {
	p.m.Lock()
	defer p.m.Unlock()

	p.byNode[node] = userID
}

// snapshot copies the register for a RUNTIME read or for the checkpoint; nil when
// nothing has completed yet, so an empty map never reaches the wire.
func (p *performers) snapshot() map[string]string {
	p.m.Lock()
	defer p.m.Unlock()

	if len(p.byNode) == 0 {
		return nil
	}

	out := make(map[string]string, len(p.byNode))
	for k, v := range p.byNode {
		out[k] = v
	}

	return out
}

// restore adopts a checkpoint's recorded performers. Without it the register would be
// lost on every hydration — and a human task is the wait most likely to dehydrate, so
// the record would vanish exactly in the case it exists for: a later node asking who
// performed an earlier task.
func (p *performers) restore(byNode map[string]string) {
	if len(byNode) == 0 {
		return
	}

	p.m.Lock()
	defer p.m.Unlock()

	for k, v := range byNode {
		p.byNode[k] = v
	}
}
