package activities

import (
	"errors"
	"strconv"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// ValidateCompensationPlacement rejects an isForCompensation activity wired
// into normal sequence flow (SRD-059 FR-2, ADR-026 §2.3): a compensation
// handler lives outside the normal flow — it is reachable only through its
// boundary's handler link and runs only when compensation is thrown. Called by
// the container Validate hooks (Process and SubProcess), fail-fast at
// registration.
func ValidateCompensationPlacement(nodes []flow.Node) error {
	ee := []error{}

	for _, n := range nodes {
		c, ok := n.(interface{ ForCompensation() bool })
		if !ok || !c.ForCompensation() {
			continue
		}

		if len(n.Incoming()) > 0 || len(n.Outgoing()) > 0 {
			ee = append(ee, errs.New(
				errs.M("compensation activity %q must not carry normal "+
					"sequence flow (isForCompensation handlers live outside "+
					"the normal flow)",
					n.Name()),
				errs.C(errorClass, errs.InvalidObject),
				errs.D("activity_id", n.ID()),
				errs.D("incoming", strconv.Itoa(len(n.Incoming()))),
				errs.D("outgoing", strconv.Itoa(len(n.Outgoing())))))
		}
	}

	if len(ee) > 0 {
		return errors.Join(ee...)
	}

	return nil
}
