package instance

import (
	"strconv"
	"sync"
)

// iterationOwners records WHO did each iteration's work: for every iterated
// activity, the ordinal that was completed and the actor who completed it
// (ADR-025 §2.15, SRD-090.D FR-4).
//
// It answers a question `COMPLETED_BY` cannot. That register keys by NODE, so
// an iterated activity has one entry however many instances ran and whoever
// did them — the last completion wins and the rest are lost. Three approvals
// are three pieces of work by three people, and "who approved item 2" has to
// be answerable after the activity has gone.
//
// **The decorator is the source, not the engine's task registry.** ADR-025
// §2.15 puts the completion account on the decorator — it is what holds which
// assignees have completed — and the decorator lives in this package, so the
// value is served without reaching across the boundary into `pkg/thresher`.
//
// Keyed by ACTIVITY ID for the reason `iterations` is: the RUNTIME name set
// stays closed, and a key disambiguates two iterated activities running at
// once, which a flat name could not.
//
// Guarded by its own mutex: entries are written on the instance-loop goroutine
// as completions are routed, and read during expression evaluation on track
// goroutines.
type iterationOwners struct {
	byActivity map[string]map[string]string
	m          sync.Mutex
}

// newIterationOwners builds an empty register.
func newIterationOwners() *iterationOwners {
	return &iterationOwners{byActivity: map[string]map[string]string{}}
}

// record notes that owner completed iteration ord of activity id.
//
// An empty owner or activity records nothing: a completion with no actor is
// not somebody's work, and an entry naming nobody would answer "who did this"
// with a blank rather than with silence.
func (o *iterationOwners) record(id string, ord int, owner string) {
	if id == "" || owner == "" {
		return
	}

	o.m.Lock()
	defer o.m.Unlock()

	byOrdinal, ok := o.byActivity[id]
	if !ok {
		byOrdinal = map[string]string{}
		o.byActivity[id] = byOrdinal
	}

	// the ordinal is the key a reader has: it is what ITERATION_NUMBER
	// publishes inside the instance and what ITERATION_ID ends with.
	byOrdinal[strconv.Itoa(ord)] = owner
}

// restore adopts the account a checkpoint recorded, so who did which iteration
// survives the instance being released and rebuilt — the ordinary case for a
// fan-out over human work, whose approvals take days.
func (o *iterationOwners) restore(byActivity map[string]map[string]string) {
	if len(byActivity) == 0 {
		return
	}

	o.m.Lock()
	defer o.m.Unlock()

	for id, byOrdinal := range byActivity {
		inner := make(map[string]string, len(byOrdinal))
		for ord, owner := range byOrdinal {
			inner[ord] = owner
		}

		o.byActivity[id] = inner
	}
}

// snapshot copies the register for a RUNTIME read; nil when nobody has
// completed an iterated iteration yet, so an empty map never reaches a reader.
func (o *iterationOwners) snapshot() map[string]map[string]string {
	o.m.Lock()
	defer o.m.Unlock()

	if len(o.byActivity) == 0 {
		return nil
	}

	out := make(map[string]map[string]string, len(o.byActivity))

	for id, byOrdinal := range o.byActivity {
		inner := make(map[string]string, len(byOrdinal))
		for ord, owner := range byOrdinal {
			inner[ord] = owner
		}

		out[id] = inner
	}

	return out
}
