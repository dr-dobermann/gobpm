// Package repository defines the Repository extension: the engine's persistence
// slot for Process Instance state. This is the MINIMAL contract sufficient to
// run today's BPMN on the in-memory architecture (ADR-001 v.4 §9). The
// production-grade contract — durable serialization, versioning/CAS,
// transactions, history/inbox/subscriptions, pagination — is owned by the
// dedicated Persistence & State ADR, not this skeleton. The in-memory default
// lives in the memrepo sibling subpackage.
package repository

import "context"

// Status is an Instance's persisted lifecycle status, mirroring the runtime
// lifecycle of ADR-001 v.4 (Active, then a terminal Completed or Terminated).
type Status int

const (
	// StatusActive marks an in-flight Instance.
	StatusActive Status = iota
	// StatusCompleted marks an Instance that finished normally.
	StatusCompleted
	// StatusTerminated marks an Instance that was canceled/terminated.
	StatusTerminated
)

// IsTerminal reports whether the status is a terminal (no longer in-flight) one.
func (s Status) IsTerminal() bool {
	return s == StatusCompleted || s == StatusTerminated
}

// InstanceRecord is the unit a Repository persists. State is the engine's
// instance snapshot, opaque to the repository (the durable serialization model
// is owned by the Persistence & State ADR); the in-memory default stores it by
// reference.
type InstanceRecord struct {
	State  any
	ID     string
	Status Status
}

// Repository persists Process Instance state. The skeleton contract is
// deliberately minimal (see the package doc); durable adapters extend it via
// the Persistence & State ADR.
type Repository interface {
	// Save stores (or replaces) the record under its ID.
	Save(ctx context.Context, rec InstanceRecord) error
	// Load returns the record for id; the bool is false when none exists.
	Load(ctx context.Context, id string) (InstanceRecord, bool, error)
	// Delete removes the record for id (a no-op if it is absent).
	Delete(ctx context.Context, id string) error
	// ListInFlight returns the IDs of all non-terminal (Active) instances, for
	// rehydration after a restart.
	ListInFlight(ctx context.Context) ([]string, error)
}
