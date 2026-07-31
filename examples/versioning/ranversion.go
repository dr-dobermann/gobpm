package main

import (
	"fmt"
	"sync"
)

// ranVersion records the release label of the greeter that actually executed.
// Every start in this example resolves to a version — StartLatest to the
// highest, StartVersion to a pinned number, StartProcess to a held handle —
// and the whole demonstration is WHICH one the engine picked. A run that
// resolved the wrong version would print a different label and complete just
// as successfully, so the label is captured and compared rather than trusted.
//
// Recorded inside the greeter operation rather than through an engine
// observer: observer facts are delivered asynchronously and can still be in
// flight when WaitCompletion returns.
type ranVersion struct {
	label string
	m     sync.Mutex
}

func newRanVersion() *ranVersion {
	return &ranVersion{}
}

// record stores the label of the greeter that just ran.
func (r *ranVersion) record(label string) {
	r.m.Lock()
	defer r.m.Unlock()

	r.label = label
}

// take returns the label of the last greeter that ran and clears it, so the
// next start cannot pass on a stale reading from the previous one.
func (r *ranVersion) take() string {
	r.m.Lock()
	defer r.m.Unlock()

	label := r.label
	r.label = ""

	return label
}

// check reports an error unless the greeter that just ran carried want.
func (r *ranVersion) check(want string) error {
	if got := r.take(); got != want {
		if got == "" {
			return fmt.Errorf("no greeter ran, want %s", want)
		}

		return fmt.Errorf("%s ran, want %s", got, want)
	}

	return nil
}
