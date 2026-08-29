package instance

import (
	"sync"

	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
)

// iterationFact is what an activity's iteration reports about itself: the
// shape, the frozen total, and how the iterations ended. It is the durable
// half of §2.9's attributes — the counts themselves stay at the activity's
// own scope, where a completionCondition and a composite body read them by
// walk-up, and end with the activation.
type iterationFact struct {
	Kind       string `json:"kind"`
	Total      int    `json:"total"`
	Completed  int    `json:"completed"`
	Terminated int    `json:"terminated"`
}

// iterations records what each iterated activity of this iteration did —
// BPMN's counts outliving the activation they describe (ADR-025 §2.9.2).
//
// It is engine-published, so it is served through the reserved read-only
// RUNTIME subtree rather than committed into the data plane: a process must be
// able to READ how many iterations ran and must not be able to overwrite the
// answer.
//
// Keyed by ACTIVITY ID rather than one runtime name per activity. Two reasons,
// and both are load-bearing: the RUNTIME name set stays closed, and — unlike a
// flat name — a key disambiguates two iterated activities running at once, a
// parallel gateway with a Multi-Instance on each arm being an ordinary model.
// That is the same reason the counts themselves cannot be flat RUNTIME names.
//
// Guarded by its own mutex: entries are written on the iteration-loop goroutine
// as an activity progresses and read during expression evaluation on track
// goroutines.
type iterations struct {
	byActivity map[string]iterationFact
	m          sync.Mutex
}

// newIterations builds an empty register.
func newIterations() *iterations {
	return &iterations{byActivity: map[string]iterationFact{}}
}

// record notes where activity id has got to. Later reports of the same
// activity replace the entry, so the register always holds the latest — and,
// once the activity ends, its final — account.
func (i *iterations) record(id string, f iterationFact) {
	if id == "" {
		return
	}

	i.m.Lock()
	defer i.m.Unlock()

	i.byActivity[id] = f
}

// records copies the register for the checkpoint, in the wire shape.
func (i *iterations) records() map[string]checkpoint.ActivityIteration {
	i.m.Lock()
	defer i.m.Unlock()

	if len(i.byActivity) == 0 {
		return nil
	}

	out := make(map[string]checkpoint.ActivityIteration, len(i.byActivity))
	for id, f := range i.byActivity {
		out[id] = checkpoint.ActivityIteration{
			Kind:       f.Kind,
			Total:      f.Total,
			Completed:  f.Completed,
			Terminated: f.Terminated,
		}
	}

	return out
}

// restore adopts the accounts a checkpoint recorded, so what an activity did
// survives the iteration being released and rebuilt — the question is asked by
// nodes AFTER it, which is exactly when a long wait has had time to dehydrate.
func (i *iterations) restore(byActivity map[string]checkpoint.ActivityIteration) {
	if len(byActivity) == 0 {
		return
	}

	i.m.Lock()
	defer i.m.Unlock()

	for id, r := range byActivity {
		i.byActivity[id] = iterationFact{
			Kind:       r.Kind,
			Total:      r.Total,
			Completed:  r.Completed,
			Terminated: r.Terminated,
		}
	}
}

// snapshot copies the register for a RUNTIME read; nil when no activity has
// iterated yet, so an empty map never reaches a reader.
func (i *iterations) snapshot() map[string]iterationFact {
	i.m.Lock()
	defer i.m.Unlock()

	if len(i.byActivity) == 0 {
		return nil
	}

	out := make(map[string]iterationFact, len(i.byActivity))
	for k, v := range i.byActivity {
		out[k] = v
	}

	return out
}
