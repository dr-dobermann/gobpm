package thresher

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/errs"
)

// AdHocHandle is the host's control surface over one open Ad-Hoc Sub-Process
// (BPMN §13.3.5, ADR-035 v.1 §2.6): it reports what the container currently
// offers for selection and what it is already running, and it performs the
// selection itself.
//
// An instance may hold several ad-hoc containers, including nested ones, so the
// handle is per-container rather than per-instance — Enabled and Activate always
// speak about the one container it names.
type AdHocHandle struct {
	inst   *InstanceHandle
	nodeID string
}

// AdHoc returns the control surface of the Ad-Hoc Sub-Process hosted by nodeID.
// The container need not be open yet: its methods report a classified
// ObjectNotFound while it is closed, so a host may hold the handle across the
// container's whole lifetime.
func (h *InstanceHandle) AdHoc(nodeID string) (*AdHocHandle, error) {
	if nodeID == "" {
		return nil, errs.New(
			errs.M("AdHoc: the container node must be named"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	return &AdHocHandle{inst: h, nodeID: nodeID}, nil
}

// Enabled reports the inner activities the container currently offers for
// selection — the enabled set a host chooses from. It is empty in automatic
// mode, where the Router's answer runs without being offered.
func (ah *AdHocHandle) Enabled(ctx context.Context) ([]string, error) {
	offered, _, err := ah.inst.current().AdHocView(ctx, ah.nodeID)

	return offered, err
}

// Running reports the inner activities the container has in flight. Under
// parallel ordering one activity may appear once per live instance.
func (ah *AdHocHandle) Running(ctx context.Context) ([]string, error) {
	_, running, err := ah.inst.current().AdHocView(ctx, ah.nodeID)

	return running, err
}

// Activate starts one offered activity — BPMN's "one enabled Activity is
// selected for execution", performed by the host. Activating an activity that
// is not currently offered is a classified error rather than a silent no-op, so
// a host acting on a stale enabled set learns of it.
func (ah *AdHocHandle) Activate(ctx context.Context, activityID string) error {
	return ah.inst.current().ActivateAdHoc(ctx, ah.nodeID, activityID)
}
