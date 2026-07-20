package events

import (
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// LinkEventDefinition is the intra-process GOTO connector (ADR-006 v.4 §2.8):
// a source Intermediate Throw event hands control to the same-name target
// Intermediate Catch event within one Process level. It is not a wait node —
// the throw redirects the token to the target's outgoing flow. Pairing is by
// name, resolved statically at snapshot build (SRD-057), so the definition
// carries only the name — the metamodel's source/target refs are not modeled
// (gobpm pairs by name at the container).
type LinkEventDefinition struct {
	name string
	definition
}

// NewLinkEventDefinition builds a Link event definition with a required
// non-empty name — the key that pairs a throw source to its catch target
// within a container. An empty name is a classified error.
func NewLinkEventDefinition(
	name string,
	baseOpts ...options.Option,
) (*LinkEventDefinition, error) {
	name = strings.TrimSpace(name)

	if err := errs.CheckStr(
		name, "a Link event requires a name", errorClass); err != nil {
		return nil, err
	}

	d, err := newDefinition(baseOpts...)
	if err != nil {
		return nil, err
	}

	return &LinkEventDefinition{
		definition: *d,
		name:       name,
	}, nil
}

// MustLinkEventDefinition is NewLinkEventDefinition that panics on error — for
// tests and static process construction.
func MustLinkEventDefinition(
	name string,
	baseOpts ...options.Option,
) *LinkEventDefinition {
	led, err := NewLinkEventDefinition(name, baseOpts...)
	if err != nil {
		errs.Panic(err)
	}

	return led
}

// Type returns the LinkEventDefinition's trigger — flow.TriggerLink.
func (*LinkEventDefinition) Type() flow.EventTrigger {
	return flow.TriggerLink
}

// Name returns the Link's pairing key.
func (l *LinkEventDefinition) Name() string {
	return l.name
}

// compile-time conformance to flow.EventDefinition.
var _ flow.EventDefinition = (*LinkEventDefinition)(nil)
