package lanes

import (
	"errors"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// ValidateLaneSets checks a container's lane sets against the nodes it actually
// holds, and is the ONLY place the engine reads a lane.
//
// A lane changes no behavior, which is exactly why it needs checking here: a
// lane referencing a node from another process would execute perfectly and
// export garbage, and registration is the only moment that error is visible.
// The same argument the value-less item-aware element and the directory-mode
// resource role already rest on (SAD-001 §14.1).
//
// Nested lane sets are checked against the SAME node set: a child lane set
// partitions its container, not a sub-container.
//
// The other normative constraint — that all lanes in one set partition by the
// same type (Table 10.135) — is enforced at construction instead, because it is
// visible in the lane set itself and needs no container to see it.
func ValidateLaneSets(sets []*LaneSet, nodes []flow.Node) error {
	known := make(map[string]struct{}, len(nodes))
	for _, n := range nodes {
		if n != nil {
			known[n.ID()] = struct{}{}
		}
	}

	ee := []error{}
	for _, ls := range sets {
		if ls == nil {
			ee = append(ee, errs.New(
				errs.M("a nil LaneSet isn't allowed"),
				errs.C(errorClass, errs.EmptyNotAllowed)))

			continue
		}

		ee = append(ee, checkSet(ls, known)...)
	}

	if len(ee) != 0 {
		return errors.Join(ee...)
	}

	return nil
}

// checkSet validates one lane set and, recursively, the lane sets nested in it.
func checkSet(ls *LaneSet, known map[string]struct{}) []error {
	ee := []error{}

	// No nil-Lane guard here: NewLaneSet refuses one at construction, so a lane
	// set cannot hold nil by the time it reaches validation. A defensive check
	// would be unreachable code.
	for _, l := range ls.Lanes() {
		for _, n := range l.FlowNodes() {
			if _, ok := known[n.ID()]; ok {
				continue
			}

			ee = append(ee, errs.New(
				errs.M("lane %q places node %q, which isn't in the container",
					l.Name(), n.Name()),
				errs.C(errorClass, errs.InvalidParameter),
				errs.D("lane", l.Name()),
				errs.D("lane_set", ls.Name())))
		}

		if child := l.ChildLaneSet(); child != nil {
			ee = append(ee, checkSet(child, known)...)
		}
	}

	return ee
}
