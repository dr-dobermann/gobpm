package events

import (
	"errors"
	"sort"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// ValidateLinkPairing checks the Link throw/catch nodes of a SINGLE
// flow-elements container (one Process level) for well-formed name-pairing
// (ADR-006 v.4 §2.8, SRD-057 §3.3): every Link name must have exactly one
// target (catch) and at least one source (throw). It is called from
// Process.Validate and SubProcess.Validate over that container's own node
// list, so the "cannot link a parent Process with a Sub-Process" rule holds by
// construction — a nested Sub-Process is one opaque node here, and its own
// Link events live only in its own node list.
//
// Errors are joined and reported in sorted link-name order (deterministic).
func ValidateLinkPairing(nodes []flow.Node) error {
	type pairing struct {
		sources, targets int
	}

	byName := map[string]*pairing{}

	for _, n := range nodes {
		name, isThrow, ok := linkNodeName(n)
		if !ok {
			continue
		}

		p := byName[name]
		if p == nil {
			p = &pairing{}
			byName[name] = p
		}

		if isThrow {
			p.sources++
		} else {
			p.targets++
		}
	}

	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}

	sort.Strings(names)

	var ee []error

	for _, name := range names {
		p := byName[name]

		switch {
		case p.targets == 0:
			ee = append(ee, linkErr(name,
				"has %d source throw(s) but no target catch", p.sources))

		case p.targets > 1:
			ee = append(ee, linkErr(name,
				"has %d target catches, expected exactly one", p.targets))
		}

		if p.sources == 0 {
			ee = append(ee, linkErr(name,
				"has a target catch but no source throw"))
		}
	}

	if len(ee) > 0 {
		return errors.Join(ee...)
	}

	return nil
}

// linkNodeName reports the Link name a node carries and whether the node is a
// throw source (true) or a catch target (false); ok is false for any node that
// does not carry a LinkEventDefinition.
func linkNodeName(n flow.Node) (name string, isThrow, ok bool) {
	switch ev := n.(type) {
	case *IntermediateThrowEvent:
		if nm, found := linkDefName(ev); found {
			return nm, true, true
		}

	case *IntermediateCatchEvent:
		if nm, found := linkDefName(ev); found {
			return nm, false, true
		}
	}

	return "", false, false
}

// linkDefName returns the name of the LinkEventDefinition on an event node, if
// the node carries one.
func linkDefName(en flow.EventNode) (string, bool) {
	for _, d := range en.Definitions() {
		if led, ok := d.(*LinkEventDefinition); ok {
			return led.Name(), true
		}
	}

	return "", false
}

// linkErr builds a classified, self-identifying Link-pairing error.
func linkErr(name, format string, args ...any) error {
	return errs.New(
		errs.M("Link %q "+format, append([]any{name}, args...)...),
		errs.C(errorClass, errs.InvalidObject),
		errs.D("link_name", name))
}
