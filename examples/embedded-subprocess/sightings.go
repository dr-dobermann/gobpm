package main

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// sightings records what each step saw when it looked up "order-id". The claim
// this example demonstrates is the scope walk-up: a task inside the embedded
// sub-process resolves a property declared on the PARENT. A step that failed to
// resolve it would print a shorter line and carry on, and the process would
// still complete — so the lookup result is what has to be checked, per step.
//
// Recorded inside the tasks rather than through an engine observer: observer
// facts are delivered asynchronously and can still be in flight when
// WaitCompletion returns.
type sightings struct {
	saw map[string]any
	m   sync.Mutex
}

func newSightings() *sightings {
	return &sightings{saw: map[string]any{}}
}

// record stores what step saw; a step that could not resolve the property
// records nil, which is what makes the failure visible.
func (s *sightings) record(step string, value any) {
	s.m.Lock()
	defer s.m.Unlock()

	s.saw[step] = value
}

// check reports an error unless every named step resolved the property to want.
func (s *sightings) check(want any, steps ...string) error {
	s.m.Lock()
	defer s.m.Unlock()

	var bad []string

	for _, st := range steps {
		got, ran := s.saw[st]
		switch {
		case !ran:
			bad = append(bad, fmt.Sprintf("%s never ran", st))
		case got != want:
			bad = append(bad, fmt.Sprintf("%s saw %v", st, got))
		}
	}

	if len(bad) > 0 {
		sort.Strings(bad)

		return fmt.Errorf("want every step to see %v: %s",
			want, strings.Join(bad, "; "))
	}

	return nil
}
