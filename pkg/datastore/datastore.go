// Package datastore defines the engine-global Data Store port (BPMN §10.4.1,
// ADR-030 §2.5): item-aware data that outlives the Process instance and is
// shared across instances within the running engine. The default in-memory
// adapter lives in the memstore subpackage; durability is a swappable adapter
// behind this interface (the future Persistence & State workstream).
package datastore

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

// DataStore is the engine-global store of item-aware data addressed by an
// opaque name. It mirrors the Repository / MessageBroker port shape: an
// interface with a default in-memory adapter, swappable for a durable one.
type DataStore interface {
	// Get returns the datum stored under name; the bool is false when none
	// exists.
	Get(ctx context.Context, name string) (data.Data, bool, error)

	// Put stores (or replaces) d under name.
	Put(ctx context.Context, name string, d data.Data) error

	// Capacity reports the store's nominal item capacity (BPMN §10.4.1). It is
	// advisory in the in-memory adapter (ADR-030 §2.6) — a durable adapter may
	// enforce it.
	Capacity() int

	// IsUnlimited reports whether the store has no capacity bound.
	IsUnlimited() bool
}

// Registry resolves an engine-global DataStore by its reference id (a BPMN
// DataStore is a Definitions-level root element; a DataStoreReference names one
// by dataStoreRef, and a process may reference many, §10.4.1). Each store has
// its own capacity and backing, so distinct dataStoreRefs never share state.
type Registry interface {
	// Store returns the DataStore registered under ref. An unregistered ref is
	// an error (fail-loud — a DataStoreReference to an unknown store is a
	// configuration mistake, not a silent auto-provision).
	Store(ref string) (DataStore, error)
}
