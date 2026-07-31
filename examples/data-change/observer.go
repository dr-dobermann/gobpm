package main

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// dataChangePrinter is an engine-scope observer that surfaces only the
// DataChange facts — the per-path change signal the commit-diff produces.
// DataChange is observer-only (never echoed to the operator log), so an
// observer like this is the way to see it.
//
// It also RECORDS what it saw, so the example can assert the change stream
// rather than only print it: a commit-diff that reported the wrong kind, the
// wrong path, or nothing at all would leave the process completing normally.
// Reading the record is safe because the example cancels the subscription
// first, and Cancel drains the facts still in flight — the asynchronous
// delivery that makes observer assertions racy elsewhere is settled by then.
type dataChangePrinter struct {
	seen []string
	m    sync.Mutex
}

// OnFact prints one line per DataChange fact: the change kind (the phase),
// the changed data path, and the node whose commit produced it.
func (p *dataChangePrinter) OnFact(f observability.Fact) {
	if f.Kind != observability.KindDataChange {
		return
	}

	change := fmt.Sprintf("%s %s @%s",
		f.Phase, f.Details[observability.AttrDataPath], f.NodeName)

	p.m.Lock()
	p.seen = append(p.seen, change)
	p.m.Unlock()

	fmt.Printf("  ▶ %s\n", change)
}

// check reports an error unless exactly these changes were observed, in order.
func (p *dataChangePrinter) check(want ...string) error {
	p.m.Lock()
	defer p.m.Unlock()

	if strings.Join(p.seen, " | ") != strings.Join(want, " | ") {
		return fmt.Errorf("saw %v, want %v", p.seen, want)
	}

	return nil
}

// checkSet reports an error unless exactly these changes were observed, in any
// order — for commits whose changes come from a map, whose iteration order is
// deliberately unspecified.
func (p *dataChangePrinter) checkSet(want ...string) error {
	p.m.Lock()
	defer p.m.Unlock()

	got := append([]string(nil), p.seen...)
	sort.Strings(got)

	w := append([]string(nil), want...)
	sort.Strings(w)

	if strings.Join(got, " | ") != strings.Join(w, " | ") {
		return fmt.Errorf("saw %v, want %v (in any order)", p.seen, want)
	}

	return nil
}
