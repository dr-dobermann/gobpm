// Package repository defines the Repository extension: the engine's
// instance-checkpoint port (ADR-033 §2.7 — ONE narrow port among peers,
// deliberately not the system's persistence facade; other storage-backed
// modules define their own ports and share the user-owned backend
// handle). The record carries an opaque, schema-versioned payload, a
// compare-and-set version and the ownership lease (ADR-033 §2.8), so
// every adapter implements the cluster-safe contract once. The in-memory
// default lives in the memrepo sibling subpackage.
package repository

import (
	"context"
	"time"
)

// Status is an Instance's persisted lifecycle status, mirroring the
// runtime lifecycle (Active, an operator-suspended hold, or a terminal
// Completed/Terminated).
type Status int

const (
	// StatusActive marks an in-flight Instance.
	StatusActive Status = iota
	// StatusCompleted marks an Instance that finished normally.
	StatusCompleted
	// StatusTerminated marks an Instance that was canceled/terminated.
	StatusTerminated
	// StatusSuspended marks an operator-suspended Instance (ADR-033 §2.6;
	// reserved by SRD-070, wired by the suspend/resume slice): dehydrated
	// state that refuses triggers until resume.
	StatusSuspended
)

// IsTerminal reports whether the status is a terminal (no longer
// in-flight) one.
func (s Status) IsTerminal() bool {
	return s == StatusCompleted || s == StatusTerminated
}

// Lease is the per-instance ownership claim (ADR-033 §2.8): the engine
// that runs the instance, its incarnation (the fencing token — a
// reclaimed instance moves to a higher incarnation, and a save carrying
// a stale one must fail), and the claim's expiry. A zero Lease means
// "unowned".
type Lease struct {
	Expiry      time.Time
	Owner       string
	Incarnation int64
}

// Expired reports whether the lease no longer holds at now (an unowned
// lease is expired by definition).
func (l Lease) Expired(now time.Time) bool {
	return l.Owner == "" || !now.Before(l.Expiry)
}

// InstanceRecord is the unit a Repository persists: the opaque,
// schema-versioned checkpoint payload (the serialization model is the
// engine's — the storage's job is bytes), the persisted status, the CAS
// record version, the ownership lease and the two partition keys of
// ADR-033 v.3 — the creator engine's group and the owning tenant.
type InstanceRecord struct {
	ID      string
	Payload []byte
	// Group is the creator engine's group (ADR-033 §2.8) and is never
	// empty: an ungrouped engine forms a single-engine group under its
	// own engine id — clustering is explicit, never accidental. Stores
	// MUST reject a record without a group.
	Group string
	// Tenant is the owning tenant's id (ADR-033 §2.7). Empty means the
	// default tenant; resolution to a concrete registry entry is the
	// store's concern, the engine stamps "" until the Multi-tenancy ADR
	// wires real assignment.
	Tenant     string
	Lease      Lease
	RecVersion int64
	Status     Status
}

// Repository persists Process Instance checkpoints. Save is
// compare-and-set: the record's RecVersion must equal the stored
// version (0 for a new record); on acceptance the store increments it.
// A mismatch MUST fail with an errs.ConcurrentUpdate-classified error —
// the split-brain fencing every adapter implements identically.
type Repository interface {
	// Save stores the record under its ID iff rec.RecVersion matches the
	// stored version (0 creates). The stored RecVersion increments on
	// success; a mismatch fails with errs.ConcurrentUpdate.
	Save(ctx context.Context, rec InstanceRecord) error
	// Load returns the record for id; the bool is false when none exists.
	Load(ctx context.Context, id string) (InstanceRecord, bool, error)
	// Delete removes the record for id (a no-op if it is absent).
	Delete(ctx context.Context, id string) error
	// ListInFlight returns the IDs of the CLAIMABLE in-flight instances
	// of the given engine group: non-terminal, not suspended, and with
	// no live lease at now — the recovery listing (ADR-033 §2.8
	// claim-first semantics, group-scoped: an engine never lists another
	// group's instances). An empty group MUST fail loud; an unregistered
	// one lists empty.
	ListInFlight(
		ctx context.Context, group string, now time.Time,
	) ([]string, error)
	// RegisterGroup establishes the engine group in the store's group
	// registry (ADR-033 §2.8), idempotently — registering an existing
	// group is a no-op. An empty group MUST fail loud.
	RegisterGroup(ctx context.Context, group string) error
	// GroupExists reports whether the group is established in the
	// registry — the membership assertion behind "join an existing
	// group only" (a misspelled group must refuse, not silently mint a
	// fresh partition). An empty group MUST fail loud.
	GroupExists(ctx context.Context, group string) (bool, error)
}
