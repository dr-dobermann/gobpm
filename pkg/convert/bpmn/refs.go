package bpmn

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// refIndex is the pass-1 result addressed by BPMN id: every element the
// document declared, ready for the pass-2 references to point at.
//
// Nodes and flows are separate spaces even though BPMN ids are unique
// across a document, because the *kind* of a reference is part of what
// the file asserts. A `default` naming a start event is a modeling
// mistake, and the resolver can only say so if it knows the id resolved
// to something of the wrong kind rather than to nothing (SRD-089.A §6
// T-9).
type refIndex struct {
	nodes map[string]flow.Node
	flows map[string]*flow.SequenceFlow
}

// newRefIndex builds the index from the assembly's pass-1 results.
func newRefIndex(asm *assembly, flows map[string]*flow.SequenceFlow) *refIndex {
	return &refIndex{nodes: asm.byID, flows: flows}
}

// pendingRef is one forward reference: an attribute naming an element
// that may not have been parsed when the attribute was read. Pass 1
// records it, pass 2 resolves it.
//
// It is an interface rather than a kind tag plus a func(any) error so
// each reference keeps its target's type through resolution — these are
// exactly the references a later element slice will get wrong, and a
// runtime cast would report that as a panic instead of a compile error.
type pendingRef interface {
	// resolve looks the reference up in the completed index and applies
	// it, or reports why it could not.
	resolve(idx *refIndex) error
}

// refSite is the referring end of a pending reference, shared by every
// kind so an unresolvable one always names both ends and the attribute
// that joined them.
type refSite struct {
	from   string // the referring element's id
	attr   string // the referring attribute, e.g. "default"
	target string // the id being referred to
}

// notFound reports a reference whose target no element declared.
func (s refSite) notFound(want string) error {
	return errs.New(
		errs.M("bpmn: %s references %s %q in %q, and no such element is declared",
			s.from, want, s.target, s.attr),
		errs.C(errorClass, errs.ObjectNotFound))
}

// wrongKind reports a reference whose target exists but is not the kind
// the attribute requires — a different error from notFound on purpose,
// because the id IS in the file and telling the author it is missing
// sends them hunting a typo that is not there.
func (s refSite) wrongKind(want, got string) error {
	return errs.New(
		errs.M("bpmn: %s references %s %q in %q, but %q is a %s",
			s.from, want, s.target, s.attr, s.target, got),
		errs.C(errorClass, errs.TypeCastingError))
}

// applyFailed reports a reference that resolved to the right element and
// was still refused by the model — a default flow that does not leave the
// gateway naming it, say. Without this the model's own error would arrive
// with no converter context, naming neither the referring element nor the
// attribute that produced it.
func (s refSite) applyFailed(err error) error {
	return errs.New(
		errs.M("bpmn: %s references %q in %q, and the model refused it",
			s.from, s.target, s.attr),
		errs.C(errorClass, errs.BulidingFailed),
		errs.E(err))
}

// flowRef defers an attribute naming a sequence flow.
type flowRef struct {
	apply func(*flow.SequenceFlow) error
	refSite
}

// resolve implements pendingRef.
func (r flowRef) resolve(idx *refIndex) error {
	sf, ok := idx.flows[r.target]
	if !ok {
		if _, isNode := idx.nodes[r.target]; isNode {
			return r.wrongKind("sequence flow", "flow node")
		}

		return r.notFound("sequence flow")
	}

	if err := r.apply(sf); err != nil {
		return r.applyFailed(err)
	}

	return nil
}

// resolveRefs runs every pending reference against the completed index,
// in the order the document declared them so a file with several broken
// references reports the first one a reader would meet.
func resolveRefs(refs []pendingRef, idx *refIndex) error {
	for _, r := range refs {
		if err := r.resolve(idx); err != nil {
			return err
		}
	}

	return nil
}
