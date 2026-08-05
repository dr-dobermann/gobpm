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
	records     map[string]*repository.InstanceRecord
	groups      map[string]struct{}
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
		records:     map[string]*repository.InstanceRecord{},
		groups:      map[string]struct{}{},
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
// with errs.ConcurrentUpdate — the fencing every adapter mirrors. The
// record must carry its creator's engine group (SRD-078 FR-1).
func (r *Repo) Save(_ context.Context, rec repository.InstanceRecord) error {
	if rec.ID == "" {
		return errs.New(
			errs.M("Save: a record needs an ID"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if rec.Group == "" {
		return errs.New(
			errs.M("Save: a record needs an engine group"),
			errs.C(errorClass, errs.EmptyNotAllowed),
			errs.D("id", rec.ID))
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.groups[rec.Group]; !ok {
		return errs.New(
			errs.M("Save: engine group %q isn't registered", rec.Group),
			errs.C(errorClass, errs.ObjectNotFound),
			errs.D("id", rec.ID))
	}

	if cur, ok := r.records[rec.ID]; ok && cur.RecVersion != rec.RecVersion {
		return errs.New(
			errs.M("Save: the record changed under the writer"),
			errs.C(errorClass, errs.ConcurrentUpdate),
			errs.D("id", rec.ID))
	}

	rec.RecVersion++
	rec.Payload = append([]byte(nil), rec.Payload...)
	r.records[rec.ID] = &rec

	if rec.Status.IsTerminal() {
		if _, tracked := r.termSet[rec.ID]; !tracked {
			r.termSet[rec.ID] = struct{}{}
			r.termOrder = append(r.termOrder, rec.ID)
			r.evictTerminalLocked()
		}
	} else if _, tracked := r.termSet[rec.ID]; tracked {
		// A terminal record revived to a non-terminal status leaves the
		// eviction ledger — an Active instance must never be evicted
		// (SRD-078 FR-9, audit remediation row 11).
		delete(r.termSet, rec.ID)
		r.termOrder = removeFirst(r.termOrder, rec.ID)
	}

	return nil
}

// Load returns a value copy of the record for id; the bool is false
// when none exists.
func (r *Repo) Load(_ context.Context, id string) (repository.InstanceRecord, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	stored, ok := r.records[id]
	if !ok {
		return repository.InstanceRecord{}, false, nil
	}

	rec := *stored
	rec.Payload = append([]byte(nil), rec.Payload...)

	return rec, true, nil
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

// ListInFlight returns the IDs of the CLAIMABLE in-flight instances of
// the given engine group — Active with no live lease at now — sorted
// for determinism (the ADR-033 §2.8 group-scoped recovery listing;
// Suspended records refuse triggers and never list).
func (r *Repo) ListInFlight(
	_ context.Context,
	group string,
	now time.Time,
) ([]string, error) {
	if group == "" {
		return nil, errs.New(
			errs.M("ListInFlight: an engine group is required"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	ids := make([]string, 0, len(r.records))
	for id, rec := range r.records {
		// Claimable is defined by EXCLUSION — non-terminal and not
		// suspended — so a growing status vocabulary (e.g. SRD-079's
		// StatusActiveIncidents) lists automatically.
		if rec.Group == group &&
			!rec.Status.IsTerminal() &&
			rec.Lease.Expired(now) {
			ids = append(ids, id)
		}
	}

	sort.Strings(ids)

	return ids, nil
}

// RegisterGroup establishes the engine group in the registry (SRD-078
// FR-1), idempotently.
func (r *Repo) RegisterGroup(_ context.Context, group string) error {
	if group == "" {
		return errs.New(
			errs.M("RegisterGroup: an engine group is required"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.groups[group] = struct{}{}

	return nil
}

// GroupExists reports whether the group is established in the registry.
func (r *Repo) GroupExists(_ context.Context, group string) (bool, error) {
	if group == "" {
		return false, errs.New(
			errs.M("GroupExists: an engine group is required"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	_, ok := r.groups[group]

	return ok, nil
}

// ClusterCompatibility declares memrepo unfit to back a multi-engine
// deployment (renv.ClusterAware, SRD-078 FR-3): the store lives in one
// process, so engines on other nodes can never see its records.
func (r *Repo) ClusterCompatibility() (bool, string) {
	return false, "in-memory; state is not shared across nodes"
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
