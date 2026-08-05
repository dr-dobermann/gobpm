package renv

import "context"

// ClusterAware is the optional capability an extension declares to
// state whether it may back a multi-engine deployment (ADR-002 §8.3,
// SRD-078 FR-3). The engine and its tooling read the declaration; the
// runtime-level cluster-mode enforcement stays with the Distribution &
// Scale work — this is the vocabulary, satisfied structurally (an
// adapter needs no import to implement it).
type ClusterAware interface {
	// ClusterCompatibility reports whether the extension is safe to
	// share between engines, with a short human-readable reason for the
	// verdict (e.g. "in-memory; state is not shared across nodes").
	ClusterCompatibility() (bool, string)
}

// Migrator is the optional capability a storage-backed extension
// implements to prepare its own objects in the shared, user-owned
// backend (ADR-033 §2.7 — "each module prepares its own objects",
// SRD-078 FR-3). thresher.Run invokes it on the wired Repository
// before the engine group is established and recovery runs; an error
// aborts the start loud — a half-created schema must never look like
// an empty store.
type Migrator interface {
	// Migrate creates or upgrades the extension's storage objects,
	// idempotently: a second run over an up-to-date backend is a no-op.
	Migrate(ctx context.Context) error
}
