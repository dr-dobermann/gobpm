// Package memrepo provides the engine's default Repository: a non-durable,
// in-memory store. Active instances are retained unconditionally (their count
// is real load); terminal records (Completed/Terminated, kept for lookup) are
// capped, evicting the oldest and warning once past the cap so they cannot grow
// unbounded (the bounded-in-memory-defaults principle, ADR-002 §4.2).
package memrepo

import (
	"context"
	"log/slog"
	"sort"
	"sync"

	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/repository"
)

// DefaultMaxTerminal is the default cap on retained terminal records.
const DefaultMaxTerminal = 1024

// Repo is an in-memory repository.Repository.
type Repo struct {
	logger      observability.Logger
	records     map[string]repository.InstanceRecord
	termSet     map[string]struct{}
	termOrder   []string
	maxTerminal int
	mu          sync.Mutex
	warnOnce    sync.Once
}

// Option configures a Repo.
type Option func(*Repo)

// WithMaxTerminal sets the cap on retained terminal records; n <= 0 disables it.
func WithMaxTerminal(n int) Option { return func(r *Repo) { r.maxTerminal = n } }

// WithLogger sets the logger used for the eviction warning.
func WithLogger(l observability.Logger) Option { return func(r *Repo) { r.logger = l } }

// New returns an in-memory Repo with the default terminal cap and
// slog.Default() logger, overridden by opts.
func New(opts ...Option) *Repo {
	r := &Repo{
		logger:      slog.Default(),
		records:     map[string]repository.InstanceRecord{},
		termSet:     map[string]struct{}{},
		maxTerminal: DefaultMaxTerminal,
	}

	for _, o := range opts {
		o(r)
	}

	return r
}

// Save stores or replaces the record under its ID.
func (r *Repo) Save(_ context.Context, rec repository.InstanceRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.records[rec.ID] = rec

	if rec.Status.IsTerminal() {
		if _, tracked := r.termSet[rec.ID]; !tracked {
			r.termSet[rec.ID] = struct{}{}
			r.termOrder = append(r.termOrder, rec.ID)
			r.evictTerminalLocked()
		}
	}

	return nil
}

// Load returns the record for id; the bool is false when none exists.
func (r *Repo) Load(_ context.Context, id string) (repository.InstanceRecord, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	rec, ok := r.records[id]

	return rec, ok, nil
}

// Delete removes the record for id (a no-op if absent).
func (r *Repo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.records, id)

	if _, ok := r.termSet[id]; ok {
		delete(r.termSet, id)
		r.termOrder = removeFirst(r.termOrder, id)
	}

	return nil
}

// ListInFlight returns the IDs of all Active instances, sorted for determinism.
func (r *Repo) ListInFlight(_ context.Context) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	ids := make([]string, 0, len(r.records))
	for id, rec := range r.records {
		if rec.Status == repository.StatusActive {
			ids = append(ids, id)
		}
	}

	sort.Strings(ids)

	return ids, nil
}

// evictTerminalLocked drops oldest terminal records past the cap. Caller holds mu.
func (r *Repo) evictTerminalLocked() {
	if r.maxTerminal <= 0 {
		return
	}

	for len(r.termOrder) > r.maxTerminal {
		oldest := r.termOrder[0]
		r.termOrder = r.termOrder[1:]
		delete(r.termSet, oldest)
		delete(r.records, oldest)

		r.warnOnce.Do(func() {
			r.logger.Warn("memrepo: terminal-record cap reached, evicting oldest",
				"cap", r.maxTerminal)
		})
	}
}

func removeFirst(s []string, v string) []string {
	for i, x := range s {
		if x == v {
			return append(s[:i], s[i+1:]...)
		}
	}

	return s
}

var _ repository.Repository = (*Repo)(nil)
