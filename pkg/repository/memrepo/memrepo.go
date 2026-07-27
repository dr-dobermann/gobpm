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
	"time"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/repository"
)

const errorClass = "MEMREPO"

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

// Save stores the record under its ID with compare-and-set semantics
// (SRD-070 FR-5): rec.RecVersion must equal the stored version (0
// creates); the stored version increments on success. A mismatch fails
// with errs.ConcurrentUpdate — the fencing every adapter mirrors.
func (r *Repo) Save(_ context.Context, rec repository.InstanceRecord) error {
	if rec.ID == "" {
		return errs.New(
			errs.M("Save: a record needs an ID"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if cur, ok := r.records[rec.ID]; ok && cur.RecVersion != rec.RecVersion {
		return errs.New(
			errs.M("Save: the record changed under the writer"),
			errs.C(errorClass, errs.ConcurrentUpdate),
			errs.D("id", rec.ID))
	}

	rec.RecVersion++
	rec.Payload = append([]byte(nil), rec.Payload...)
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

// Load returns a value copy of the record for id; the bool is false
// when none exists.
func (r *Repo) Load(_ context.Context, id string) (repository.InstanceRecord, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	rec, ok := r.records[id]
	if ok {
		rec.Payload = append([]byte(nil), rec.Payload...)
	}

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

// ListInFlight returns the IDs of the CLAIMABLE in-flight instances —
// Active with no live lease at now — sorted for determinism (the
// ADR-033 §2.8 recovery listing; Suspended records refuse triggers and
// never list).
func (r *Repo) ListInFlight(_ context.Context, now time.Time) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	ids := make([]string, 0, len(r.records))
	for id, rec := range r.records {
		if rec.Status == repository.StatusActive && rec.Lease.Expired(now) {
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
