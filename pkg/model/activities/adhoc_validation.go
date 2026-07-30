package activities

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// adHocAdmitted maps a node's type to why it is not admitted inside an Ad-Hoc
// Sub-Process in this slice; an absent key is admitted. A table rather than a
// switch: the containment rule is a classification, and the second slice lifts
// entries out of it rather than editing branches (SRD-074 §3.3).
var adHocRejected = map[flow.NodeType]string{
	flow.GatewayNodeType: "a gateway routes tokens along sequence flows, and " +
		"an ad-hoc container has none — routing is the Router's answer",
	flow.EventNodeType: "events inside an ad-hoc container arrive with the " +
		"token-flow slice; a start or end event never belongs in one " +
		"(BPMN §13.3.5 admits only intermediate events)",
}

// validateAdHocShape enforces the Ad-Hoc containment rule (ADR-035 v.1 §2.8):
// this slice admits leaf Tasks and plain embedded Sub-Processes, so an inner
// element the Router could not sensibly select — a gateway, an event, a
// sequence flow — is rejected by name rather than accepted and ignored.
//
// The "a container must state how it finishes" rule (§2.9) is enforced earlier,
// at construction: WithAdHoc rejects a nil Router and every refining option
// requires it, so a routerless ad-hoc container cannot be built in the first
// place and needs no check here.
func (sp *SubProcess) validateAdHocShape(ee *[]error) {
	if fc := len(sp.Flows()); fc != 0 {
		*ee = append(*ee, errs.New(
			errs.M("ad-hoc Sub-Process %q holds %d sequence flow(s): inner "+
				"activities are ordered by the Router, and flow-driven "+
				"succession inside an ad-hoc container arrives with the "+
				"token-flow slice", sp.Name(), fc),
			errs.C(errorClass, errs.InvalidObject)))
	}

	for _, n := range sp.Nodes() {
		sp.checkAdHocNode(ee, n)
	}
}

// checkAdHocNode rejects one inadmissible inner element, naming what it found
// and why — a Sub-Process variant is reported as the variant, not as "a
// Sub-Process", so the message points at the real problem.
func (sp *SubProcess) checkAdHocNode(ee *[]error, n flow.Node) {
	if reason, rejected := adHocRejected[n.NodeType()]; rejected {
		*ee = append(*ee, errs.New(
			errs.M("ad-hoc Sub-Process %q holds %q: %s",
				sp.Name(), n.Name(), reason),
			errs.C(errorClass, errs.InvalidObject)))

		return
	}

	inner, isSub := n.(*SubProcess)
	if !isSub {
		return
	}

	reason := ""

	switch {
	case inner.IsEventSubProcess():
		reason = "an Event Sub-Process is a scope-armed handler, not an " +
			"activity a Router can select"

	case inner.IsTransaction():
		reason = "a Transaction inside an ad-hoc container is out of scope — " +
			"its Cancel abort has no defined meaning against a Router"
	}

	if reason != "" {
		*ee = append(*ee, errs.New(
			errs.M("ad-hoc Sub-Process %q holds %q: %s",
				sp.Name(), n.Name(), reason),
			errs.C(errorClass, errs.InvalidObject)))
	}
}
