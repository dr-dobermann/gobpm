// Package memstore provides the engine's default DataStore: a non-durable,
// in-memory, concurrency-safe store of item-aware data by name (ADR-030 §2.5).
// Capacity is advisory — a Put past a nominal capacity is not rejected
// (ADR-030 §2.6); a durable adapter may enforce it.
package memstore

import (
	"context"
	"sync"

	"github.com/dr-dobermann/gobpm/pkg/datastore"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

const errorClass = "MEMSTORE_ERROR"

// Unlimited is the capacity value denoting no bound (the default).
const Unlimited = 0

// Store is an in-memory datastore.DataStore.
type Store struct {
	items    map[string]data.Data
	capacity int
	mu       sync.RWMutex
}

// Option configures a Store.
type Option func(*Store)

// WithCapacity sets the store's nominal capacity (advisory, ADR-030 §2.6);
// n <= 0 leaves it Unlimited.
func WithCapacity(n int) Option {
	return func(s *Store) {
		if n > 0 {
			s.capacity = n
		}
	}
}

// New returns an in-memory Store, unbounded unless WithCapacity is given.
func New(opts ...Option) *Store {
	s := &Store{
		items:    map[string]data.Data{},
		capacity: Unlimited,
	}

	for _, o := range opts {
		o(s)
	}

	return s
}

// Get returns the datum stored under name; the bool is false when none exists.
func (s *Store) Get(_ context.Context, name string) (data.Data, bool, error) {
	if name == "" {
		return nil, false, errs.New(
			errs.M("memstore.Get: an empty name isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	d, ok := s.items[name]

	return d, ok, nil
}

// Put stores (or replaces) d under name. Capacity is advisory — a Put past the
// nominal capacity is accepted, not rejected (ADR-030 §2.6).
func (s *Store) Put(_ context.Context, name string, d data.Data) error {
	if name == "" {
		return errs.New(
			errs.M("memstore.Put: an empty name isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if d == nil {
		return errs.New(
			errs.M("memstore.Put: a nil datum isn't allowed for %q", name),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.items[name] = d

	return nil
}

// Capacity reports the store's nominal capacity (advisory, ADR-030 §2.6).
func (s *Store) Capacity() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.capacity
}

// IsUnlimited reports whether the store has no capacity bound.
func (s *Store) IsUnlimited() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.capacity == Unlimited
}

var _ datastore.DataStore = (*Store)(nil)
