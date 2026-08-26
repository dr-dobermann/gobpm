package bpmn

import (
	"encoding/xml"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/lanes"
)

// laneSetSpec is a <laneSet> as read: its lanes, and for each of them the
// node ids it places. The nodes do not exist yet — pass 1 reads a
// document, and a flow node is constructed in pass 2 (SRD-089.E §4.3).
type laneSetSpec struct {
	id, name string
	lanes    []laneSpec
}

// laneSpec is one <lane>, with the ids of the nodes on it and the lane
// set nested under it, if any.
type laneSpec struct {
	child    *laneSetSpec
	id, name string
	nodeRefs []string
}

// placement pairs a built lane with the ids it still has to place. It
// exists because a lane names nodes, a lane set is a CONSTRUCTION option
// of its container, and the nodes are not built when the container is —
// so placement is the one thing that has to wait (§4.3).
type placement struct {
	lane     *lanes.Lane
	nodeRefs []string
}

// parseLaneSet reads one <laneSet> and everything under it. A declared id
// joins the document's one ledger (SRD-089.F §4.11) — before this claim, a
// lane set could silently reuse a task's id.
func (p *parser) parseLaneSet(se xml.StartElement) (laneSetSpec, error) {
	ls := laneSetSpec{
		id:   strings.TrimSpace(attrValue(se, "id")),
		name: strings.TrimSpace(attrValue(se, "name")),
	}

	if ls.id != "" {
		if err := p.claimID(ls.id, se.Name.Local); err != nil {
			return laneSetSpec{}, err
		}
	}

	for {
		tok, err := p.token()
		if err != nil {
			return laneSetSpec{}, err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Space != nsBPMN || t.Name.Local != tagLane {
				if err := p.skipElement(); err != nil {
					return laneSetSpec{}, err
				}

				continue
			}

			l, err := p.parseLane(t)
			if err != nil {
				return laneSetSpec{}, err
			}

			ls.lanes = append(ls.lanes, l)

		case xml.EndElement:
			if t.Name == se.Name {
				return ls, nil
			}
		}
	}
}

// parseLane reads one <lane>: its flowNodeRefs and its childLaneSet. A
// declared id joins the one ledger like the lane set's.
func (p *parser) parseLane(se xml.StartElement) (laneSpec, error) {
	l := laneSpec{
		id:   strings.TrimSpace(attrValue(se, "id")),
		name: strings.TrimSpace(attrValue(se, "name")),
	}

	if l.id != "" {
		if err := p.claimID(l.id, se.Name.Local); err != nil {
			return laneSpec{}, err
		}
	}

	for {
		tok, err := p.token()
		if err != nil {
			return laneSpec{}, err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			if err := p.parseLaneChild(&l, t); err != nil {
				return laneSpec{}, err
			}

		case xml.EndElement:
			if t.Name == se.Name {
				return l, nil
			}
		}
	}
}

// parseLaneChild routes one child of a <lane>.
func (p *parser) parseLaneChild(l *laneSpec, se xml.StartElement) error {
	if se.Name.Space != nsBPMN {
		return p.skipElement()
	}

	switch se.Name.Local {
	case tagFlowNodeRef:
		ref, err := p.readText(se)
		if err != nil {
			return err
		}

		if ref = strings.TrimSpace(ref); ref != "" {
			l.nodeRefs = append(l.nodeRefs, ref)
		}

		return nil

	case tagChildLaneSet:
		child, err := p.parseLaneSet(se)
		if err != nil {
			return err
		}

		l.child = &child

		return nil
	}

	return p.skipElement()
}

// buildLaneSets turns the specs into model lane sets, recording each
// lane's pending placements on the assembly.
//
// The lanes are built EMPTY of nodes. A lane set is a construction option
// of its container and the container is built before its children exist,
// so the ids are placed afterwards by placeLaneNodes — the same
// read-now-resolve-later shape the deferred node build uses (§4.3).
func buildLaneSets(
	asm *assembly, specs []laneSetSpec,
) ([]*lanes.LaneSet, error) {
	if len(specs) == 0 {
		return nil, nil
	}

	out := make([]*lanes.LaneSet, 0, len(specs))

	for _, s := range specs {
		set, err := buildLaneSet(asm, s)
		if err != nil {
			return nil, err
		}

		out = append(out, set)
	}

	return out, nil
}

// buildLaneSet builds one lane set and the lanes under it. Each built
// lane and set with a declared id joins the assembly's lane lookup, so
// an association can end on it (SRD-092 M5) — a lane is model-held, and
// ADR-039 §2.6 reserves the report degradation for what the model does
// NOT hold. An id is 0..1 on both elements, so one without an id is
// carried under a generated id (and, unreferencable, joins no lookup).
func buildLaneSet(
	asm *assembly, s laneSetSpec,
) (*lanes.LaneSet, error) {
	built := make([]*lanes.Lane, 0, len(s.lanes))

	for _, ls := range s.lanes {
		var child *lanes.LaneSet

		if ls.child != nil {
			c, err := buildLaneSet(asm, *ls.child)
			if err != nil {
				return nil, err
			}

			child = c
		}

		// A lane's partitionElement is a resource reference the document
		// carries as partitionElementRef into a <resource> this converter
		// does not map; the lane itself is model-only either way.
		l, err := lanes.NewLane(fallbackName(ls.id, ls.name), nil, "", child,
			withDeclaredID(ls.id)...)
		if err != nil {
			return nil, errs.New(
				errs.M("bpmn: couldn't create lane %q", ls.id),
				errs.C(errorClass, errs.BulidingFailed),
				errs.E(err))
		}

		if ls.id != "" {
			asm.lanesByID[ls.id] = l
		}

		if len(ls.nodeRefs) != 0 {
			asm.places = append(asm.places,
				placement{lane: l, nodeRefs: ls.nodeRefs})
		}

		built = append(built, l)
	}

	set, err := lanes.NewLaneSet(fallbackName(s.id, s.name), built,
		withDeclaredID(s.id)...)
	if err != nil {
		return nil, errs.New(
			errs.M("bpmn: couldn't create lane set %q", s.id),
			errs.C(errorClass, errs.BulidingFailed),
			errs.E(err))
	}

	if s.id != "" {
		asm.lanesByID[s.id] = set
	}

	return set, nil
}

// placeLaneNodes resolves every <flowNodeRef> against the finished id
// table and places the node on its lane.
//
// A ref naming nothing in the document is refused HERE, because the model
// cannot: Place takes nodes, so an id that resolves to nothing would
// simply not be placed, and the lane would come out quietly smaller than
// the file drew. A ref naming a node in a DIFFERENT container is placed
// and refused by ValidateLaneSets, whose message says which container —
// the rule is the model's and stays there (NFR-4).
func placeLaneNodes(asm *assembly) error {
	for _, pl := range asm.places {
		nodes := make([]flow.Node, 0, len(pl.nodeRefs))

		for _, ref := range pl.nodeRefs {
			n, ok := asm.byID[ref]
			if !ok {
				return errs.New(
					errs.M("bpmn: lane %q places %q through <flowNodeRef>, "+
						"and no flow node with that id is declared",
						pl.lane.Name(), ref),
					errs.C(errorClass, errs.ObjectNotFound))
			}

			nodes = append(nodes, n)
		}

		// Unreachable from any document: Place refuses exactly one thing,
		// a nil node (lane.go:109-114), and every node above came out of
		// the id table with ok — which buildNodes fills with constructed
		// nodes only. Said in the form the coverage gate reads.
		if err := pl.lane.Place(nodes...); err != nil {
			return errs.Invariant(
				"lane %q rejected a node from the id table: %w",
				pl.lane.Name(), err)
		}
	}

	return nil
}
