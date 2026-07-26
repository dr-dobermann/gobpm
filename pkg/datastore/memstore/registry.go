package memstore

import (
	"sync"

	"github.com/dr-dobermann/gobpm/pkg/datastore"
	"github.com/dr-dobermann/gobpm/pkg/errs"
)

// Registry is the default in-memory datastore.Registry: a set of named
// DataStores registered up front (each with its own capacity/backing). An
// unregistered ref is an error — a DataStoreReference to an unknown store is a
// configuration mistake, not a silent auto-provision (SRD-068 FR-2).
type Registry struct {
	stores map[string]datastore.DataStore
	mu     sync.RWMutex
}

// NewRegistry returns an empty in-memory registry. Stores are added with
// Register (or the thresher's WithDataStore option).
func NewRegistry() *Registry {
	return &Registry{stores: map[string]datastore.DataStore{}}
}

// Register adds store under ref, replacing any prior registration. An empty ref
// or a nil store is rejected.
func (r *Registry) Register(ref string, store datastore.DataStore) error {
	if ref == "" {
		return errs.New(
			errs.M("memstore.Register: an empty store ref isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if store == nil {
		return errs.New(
			errs.M("memstore.Register: a nil store isn't allowed for %q", ref),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.stores[ref] = store

	return nil
}

// Store returns the DataStore registered under ref, or an error if none is
// registered (fail-loud, SRD-068 FR-2).
func (r *Registry) Store(ref string) (datastore.DataStore, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	s, ok := r.stores[ref]
	if !ok {
		return nil, errs.New(
			errs.M("memstore: no DataStore registered for ref %q", ref),
			errs.C(errorClass, errs.ObjectNotFound))
	}

	return s, nil
}

var _ datastore.Registry = (*Registry)(nil)
