// Package auth defines the AuthorizationProvider extension: the engine's
// authorization slot for sensitive operations. The default is allow-all (the
// library delegates authorization to the host application — ADR-002 §4.2/§6);
// it lives in the allowall sibling subpackage. Enforcement call-sites are not
// wired in the skeleton (SRD-004 N2), and a production authorization contract
// is owned by its own ADR (ADR-001 v.4 §9).
package auth

import "context"

// Action identifies a sensitive operation subject to authorization.
type Action string

const (
	// ActionStartProcess covers starting a Process Instance.
	ActionStartProcess Action = "process.start"
	// ActionClaimUserTask covers claiming a UserTask.
	ActionClaimUserTask Action = "usertask.claim"
	// ActionCancelInstance covers canceling/terminating an Instance.
	ActionCancelInstance Action = "instance.cancel"
)

// Request describes an authorization decision to make.
type Request struct {
	// Subject is the actor's identity, opaque to the engine.
	Subject string
	// Resource is the target the action applies to (e.g. a process or
	// instance ID).
	Resource string
	// Action is the operation being attempted.
	Action Action
}

// AuthorizationProvider authorizes sensitive operations.
type AuthorizationProvider interface {
	// Authorize returns nil when the request is allowed, or an error
	// describing the denial.
	Authorize(ctx context.Context, req Request) error
}
