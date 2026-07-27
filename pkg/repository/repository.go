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
// record version and the ownership lease.
type InstanceRecord struct {
	ID         string
	Payload    []byte
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
	// ListInFlight returns the IDs of the CLAIMABLE in-flight instances:
	// non-terminal, not suspended, and with no live lease at now — the
	// recovery listing (ADR-033 §2.8 claim-first semantics).
	ListInFlight(ctx context.Context, now time.Time) ([]string, error)
}
