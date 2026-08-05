package memrepo

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/repository"
)

type capLogger struct{ warns int }

func (l *capLogger) Debug(string, ...any) {}
func (l *capLogger) Info(string, ...any)  {}
func (l *capLogger) Warn(string, ...any)  { l.warns++ }
func (l *capLogger) Error(string, ...any) {}

// testGroup is the engine group the test records live in (SRD-078
// FR-1: records reference established groups only).
const testGroup = "test-group"

func rec(id string, st repository.Status) repository.InstanceRecord {
	return repository.InstanceRecord{
		Payload: []byte(id), ID: id, Status: st, Group: testGroup,
	}
}

// newRepo builds a Repo with testGroup established — the engine
// sequence (RegisterGroup at Run) every save relies on.
func newRepo(t *testing.T, opts ...Option) *Repo {
	t.Helper()

	r := New(opts...)
	if err := r.RegisterGroup(context.Background(), testGroup); err != nil {
		t.Fatal(err)
	}

	return r
}

// resave loads the current version and saves the record over it (the
// CAS-correct overwrite the tests use).
func resave(t *testing.T, r *Repo, nr repository.InstanceRecord) {
	t.Helper()

	ctx := context.Background()

	cur, ok, err := r.Load(ctx, nr.ID)
	if err != nil {
		t.Fatal(err)
	}

	if ok {
		nr.RecVersion = cur.RecVersion
	}

	if err := r.Save(ctx, nr); err != nil {
		t.Fatal(err)
	}
}

func TestSaveLoadDelete(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()

	_ = r.Save(ctx, rec("a", repository.StatusActive))

	got, ok, _ := r.Load(ctx, "a")
	if !ok || got.ID != "a" {
		t.Fatalf("Load(a) = %+v, %v", got, ok)
	}

	if _, ok, _ := r.Load(ctx, "missing"); ok {
		t.Fatal("missing record reported as found")
	}

	_ = r.Delete(ctx, "a")
	if _, ok, _ := r.Load(ctx, "a"); ok {
		t.Fatal("deleted record still present")
	}

	_ = r.Delete(ctx, "absent") // no-op, must not panic
}

func TestListInFlightOnlyActiveSorted(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()

	_ = r.Save(ctx, rec("b", repository.StatusActive))
	_ = r.Save(ctx, rec("a", repository.StatusActive))
	_ = r.Save(ctx, rec("c", repository.StatusCompleted))

	ids, _ := r.ListInFlight(ctx, testGroup, time.Now())
	if len(ids) != 2 || ids[0] != "a" || ids[1] != "b" {
		t.Fatalf("in-flight = %v, want [a b]", ids)
	}
}

func TestActiveNeverEvictedTerminalCapped(t *testing.T) {
	lg := &capLogger{}
	r := newRepo(t, WithMaxTerminal(2), WithLogger(lg))
	ctx := context.Background()

	_ = r.Save(ctx, rec("active", repository.StatusActive))
	_ = r.Save(ctx, rec("t1", repository.StatusCompleted))
	_ = r.Save(ctx, rec("t2", repository.StatusTerminated))
	_ = r.Save(ctx, rec("t3", repository.StatusCompleted)) // cap 2 -> evict t1

	if _, ok, _ := r.Load(ctx, "t1"); ok {
		t.Fatal("t1 should have been evicted")
	}

	for _, id := range []string{"t2", "t3", "active"} {
		if _, ok, _ := r.Load(ctx, id); !ok {
			t.Fatalf("%s should be retained", id)
		}
	}

	if lg.warns != 1 {
		t.Fatalf("warns = %d, want exactly 1", lg.warns)
	}
}

func TestReSaveTerminalNotDoubleTracked(t *testing.T) {
	r := newRepo(t, WithMaxTerminal(1))
	ctx := context.Background()

	_ = r.Save(ctx, rec("t1", repository.StatusCompleted))
	resave(t, r, rec("t1", repository.StatusCompleted))    // re-save: still one series
	_ = r.Save(ctx, rec("t2", repository.StatusCompleted)) // now two -> evict t1

	if _, ok, _ := r.Load(ctx, "t1"); ok {
		t.Fatal("t1 should be evicted once t2 is saved")
	}

	if _, ok, _ := r.Load(ctx, "t2"); !ok {
		t.Fatal("t2 should be retained")
	}
}

// TestTerminalRevivedToActiveNotEvicted is the SRD-078 FR-9 regression
// (audit remediation row 11): a terminal record re-saved Active leaves
// the eviction ledger, so cap pressure can never evict a live instance.
func TestTerminalRevivedToActiveNotEvicted(t *testing.T) {
	r := newRepo(t, WithMaxTerminal(1))
	ctx := context.Background()

	_ = r.Save(ctx, rec("i", repository.StatusCompleted)) // tracked terminal
	resave(t, r, rec("i", repository.StatusActive))       // revived → untracked

	// Fill the cap with real terminals; the revived record must survive.
	_ = r.Save(ctx, rec("t1", repository.StatusCompleted))
	_ = r.Save(ctx, rec("t2", repository.StatusCompleted))

	if _, ok, _ := r.Load(ctx, "i"); !ok {
		t.Fatal("an Active record must never be evicted (FR-9)")
	}

	ids, _ := r.ListInFlight(ctx, testGroup, time.Now())
	if len(ids) != 1 || ids[0] != "i" {
		t.Fatalf("in-flight = %v, want [i]", ids)
	}
}

func TestDeleteTerminalUntracks(t *testing.T) {
	r := newRepo(t, WithMaxTerminal(2))
	ctx := context.Background()

	_ = r.Save(ctx, rec("t1", repository.StatusCompleted))
	_ = r.Delete(ctx, "t1") // untracks from the terminal order

	_ = r.Save(ctx, rec("t2", repository.StatusCompleted))
	_ = r.Save(ctx, rec("t3", repository.StatusCompleted))

	for _, id := range []string{"t2", "t3"} {
		if _, ok, _ := r.Load(ctx, id); !ok {
			t.Fatalf("%s should be retained", id)
		}
	}
}

func TestMaxTerminalDisabled(t *testing.T) {
	r := newRepo(t, WithMaxTerminal(0))
	ctx := context.Background()

	for i := range 5 {
		_ = r.Save(ctx, rec(strconv.Itoa(i), repository.StatusCompleted))
	}

	kept := 0
	for i := range 5 {
		if _, ok, _ := r.Load(ctx, strconv.Itoa(i)); ok {
			kept++
		}
	}

	if kept != 5 {
		t.Fatalf("kept = %d, want 5 (cap disabled)", kept)
	}
}

func TestRemoveFirst(t *testing.T) {
	got := removeFirst([]string{"a", "b", "c"}, "b")
	if len(got) != 2 || got[0] != "a" || got[1] != "c" {
		t.Fatalf("removeFirst = %v, want [a c]", got)
	}

	same := removeFirst([]string{"a"}, "x") // absent -> unchanged
	if len(same) != 1 || same[0] != "a" {
		t.Fatalf("removeFirst(absent) = %v, want [a]", same)
	}
}

func TestCASAndLease(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	now := time.Now()

	// create at version 0; the store bumps to 1.
	first := rec("i", repository.StatusActive)
	first.Lease = repository.Lease{
		Owner: "engine-A", Incarnation: 1, Expiry: now.Add(time.Minute),
	}

	if err := r.Save(ctx, first); err != nil {
		t.Fatal(err)
	}

	got, ok, _ := r.Load(ctx, "i")
	if !ok || got.RecVersion != 1 {
		t.Fatalf("RecVersion = %d, want 1", got.RecVersion)
	}

	if got.Lease.Owner != "engine-A" || got.Lease.Incarnation != 1 {
		t.Fatalf("lease didn't round-trip: %+v", got.Lease)
	}

	// a stale writer (still at version 0) is fenced.
	stale := rec("i", repository.StatusActive)

	err := r.Save(ctx, stale)
	if err == nil {
		t.Fatal("a stale save must be rejected")
	}

	var ae *errs.ApplicationError
	if !errors.As(err, &ae) || !ae.HasClass(errs.ConcurrentUpdate) {
		t.Fatalf("want a ConcurrentUpdate-classified error, got %v", err)
	}

	// the current writer succeeds and the version advances.
	got.Status = repository.StatusActive
	if err := r.Save(ctx, got); err != nil {
		t.Fatal(err)
	}

	got2, _, _ := r.Load(ctx, "i")
	if got2.RecVersion != 2 {
		t.Fatalf("RecVersion = %d, want 2", got2.RecVersion)
	}
}

func TestListInFlightHonorsLeases(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	now := time.Now()

	held := rec("held", repository.StatusActive)
	held.Lease = repository.Lease{
		Owner: "engine-A", Incarnation: 1, Expiry: now.Add(time.Minute),
	}

	lapsed := rec("lapsed", repository.StatusActive)
	lapsed.Lease = repository.Lease{
		Owner: "engine-B", Incarnation: 1, Expiry: now.Add(-time.Minute),
	}

	free := rec("free", repository.StatusActive)
	susp := rec("susp", repository.StatusSuspended)

	for _, x := range []repository.InstanceRecord{held, lapsed, free, susp} {
		if err := r.Save(ctx, x); err != nil {
			t.Fatal(err)
		}
	}

	ids, _ := r.ListInFlight(ctx, testGroup, now)
	if len(ids) != 2 || ids[0] != "free" || ids[1] != "lapsed" {
		t.Fatalf("claimable = %v, want [free lapsed]", ids)
	}
}

func TestPayloadIsCopied(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()

	orig := rec("c", repository.StatusActive)
	orig.Payload = []byte("checkpoint")

	if err := r.Save(ctx, orig); err != nil {
		t.Fatal(err)
	}

	orig.Payload[0] = 'X' // mutating the caller's slice must not leak in

	got, _, _ := r.Load(ctx, "c")
	if !bytes.Equal(got.Payload, []byte("checkpoint")) {
		t.Fatalf("stored payload mutated: %q", got.Payload)
	}

	got.Payload[0] = 'Y' // mutating the loaded copy must not leak back

	again, _, _ := r.Load(ctx, "c")
	if !bytes.Equal(again.Payload, []byte("checkpoint")) {
		t.Fatalf("loaded copy leaked back: %q", again.Payload)
	}
}

func TestSaveValidation(t *testing.T) {
	r := newRepo(t)

	if err := r.Save(context.Background(),
		repository.InstanceRecord{}); err == nil {
		t.Fatal("an ID-less record must be rejected")
	}
}
