package activities

import (
	"errors"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	hi "github.com/dr-dobermann/gobpm/pkg/model/hinteraction"
	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// roleHolder is any element carrying declared resource roles. Activities get
// Roles() from the embedded activity; a container passes its own roles in
// separately, since it is not one of its own nodes.
type roleHolder interface {
	Roles() []*hi.ResourceRole
}

// ValidateResourceRoles rejects an authorizing-kind role that names its people
// through a directory query (ADR-020 v.3 §2.5.4, SRD-075 FR-5).
//
// Resolving a resourceRef needs an Organizational Directory (BPMN §8.4.12
// Resources) that the engine does not provide, so such a role could only be
// carried and ignored — declared authorization that authorizes nobody. It is
// refused at registration instead, on the same principle as the value-less
// item-aware element (SAD-001 §14.1): a declaration the engine can never
// satisfy is refused at build time rather than admitted and silently ignored at
// run time.
//
// Declarative kinds are not checked. A bare ResourceRole or a Performer grants
// nothing whether or not it resolves, so a directory-held resource named there
// is a conformant annotation — Table 10.3 describes exactly that use.
//
// nodes are the container's flow nodes; ownRoles are the roles declared on the
// container itself. A Sub-Process passes nil for ownRoles, because its own roles
// are checked by the parent that holds it as a node — otherwise one role would
// be reported twice.
func ValidateResourceRoles(
	nodes []flow.Node,
	ownRoles []*hi.ResourceRole,
) error {
	ee := checkRoles("process", "", ownRoles)

	for _, n := range nodes {
		rh, ok := n.(roleHolder)
		if !ok {
			continue
		}

		ee = append(ee, checkRoles(n.Name(), n.ID(), rh.Roles())...)
	}

	if len(ee) != 0 {
		return errors.Join(ee...)
	}

	return nil
}

// checkRoles returns one error per directory-mode authorizing role of holder,
// naming the role, the element carrying it and the missing subsystem.
func checkRoles(
	holderName, holderID string,
	roles []*hi.ResourceRole,
) []error {
	ee := []error{}

	for _, r := range roles {
		if r == nil || !r.Kind().Authorizes() || r.Resource() == nil {
			continue
		}

		ee = append(ee, errs.New(
			errs.M("%s %q on %q resolves its people through a resource "+
				"reference, which needs an organizational directory the "+
				"engine doesn't provide; use an assignment expression instead",
				r.Kind(), r.Name(), holderName),
			errs.C(errorClass, errs.InvalidParameter),
			errs.D("role", r.Name()),
			errs.D("kind", string(r.Kind())),
			errs.D(observability.AttrNodeID, holderID)))
	}

	return ee
}
