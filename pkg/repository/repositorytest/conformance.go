// Package repositorytest publishes the Repository conformance suite
// (ADR-002 §8.4, ADR-003 §4.2): every Repository implementation — the
// in-memory default and any durable adapter — proves the same contract
// by calling Conformance from a one-line test. The suite covers the
// CAS discipline, the ADR-033 §2.8 group scoping, lease and tenant
// round-trips, payload isolation and the recovery-listing filters.
package repositorytest

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/repository"
)

// Factory builds a fresh, empty Repository under test. It is called
// once per subtest, so implementations must return isolated stores
// (for a shared backend: a wiped namespace).
type Factory func(t *testing.T) repository.Repository

// Conformance runs the full Repository contract against factory-built
// stores. Adapter tests are one-liners:
//
//	func TestConformance(t *testing.T) {
//		repositorytest.Conformance(t, func(t *testing.T) repository.Repository {
//			return memrepo.New()
//		})
//	}
func Conformance(t *testing.T, factory Factory) {
	t.Helper()

	if factory == nil {
		t.Fatal("Conformance: a nil Factory isn't allowed")
	}

	for name, test := range conformanceTests {
		t.Run(name, func(t *testing.T) { test(t, factory(t)) })
	}
}

// now is an arbitrary fixed instant — the suite drives time explicitly.
var now = time.Date(2026, time.August, 4, 12, 0, 0, 0, time.UTC)

// rec is the baseline valid record the subtests derive from.
func rec(id string) repository.InstanceRecord {
	return repository.InstanceRecord{
		ID:      id,
		Payload: []byte(`{"schema":1}`),
		Group:   "conformance-group",
		Status:  repository.StatusActive,
	}
}

// conformanceTests is the contract as a declarative table.
var conformanceTests = map[string]func(*testing.T, repository.Repository){
	"CASCreateAndUpdate":            testCASCreateAndUpdate,
	"CASStaleVersionRejected":       testCASStaleVersionRejected,
	"SaveWithoutIDRejected":         testSaveWithoutIDRejected,
	"SaveWithoutGroupRejected":      testSaveWithoutGroupRejected,
	"SaveUnregisteredGroupRejected": testSaveUnregisteredGroupRejected,
	"GroupRegistryIdempotent":       testGroupRegistryIdempotent,
	"GroupExistsUnknown":            testGroupExistsUnknown,
	"RegistryEmptyGroupRejected":    testRegistryEmptyGroupRejected,
	"LoadFidelity":                  testLoadFidelity,
	"LoadAbsent":                    testLoadAbsent,
	"PayloadIsolation":              testPayloadIsolation,
	"DeleteIdempotent":              testDeleteIdempotent,
	"ListFilters":                   testListFilters,
	"ListGroupScoped":               testListGroupScoped,
	"ListEmptyGroupRejected":        testListEmptyGroupRejected,
	"ListUnregisteredGroupEmpty":    testListUnregisteredGroupEmpty,
	"ListDeterministicOrder":        testListDeterministicOrder,
}

func testCASCreateAndUpdate(t *testing.T, r repository.Repository) {
	ctx := context.Background()

	mustRegister(t, r)

	if err := r.Save(ctx, rec("i1")); err != nil {
		t.Fatalf("create (RecVersion 0): %v", err)
	}

	got, ok, err := r.Load(ctx, "i1")
	if err != nil || !ok {
		t.Fatalf("load after create: ok=%v err=%v", ok, err)
	}

	if got.RecVersion != 1 {
		t.Fatalf("stored RecVersion after create = %d, want 1", got.RecVersion)
	}

	got.Status = repository.StatusCompleted
	if err := r.Save(ctx, got); err != nil {
		t.Fatalf("update at matching version: %v", err)
	}

	got = loaded(t, r)
	if got.RecVersion != 2 || got.Status != repository.StatusCompleted {
		t.Fatalf("after update: version=%d status=%v, want 2/Completed", got.RecVersion, got.Status)
	}
}

func testCASStaleVersionRejected(t *testing.T, r repository.Repository) {
	ctx := context.Background()

	base := rec("i1")
	mustSave(t, r, base)         // stored version 1
	mustSave(t, r, loaded(t, r)) // stored version 2

	stale := base // RecVersion 0 — two writes behind
	wantConcurrentUpdate(t, r.Save(ctx, stale))

	if got := loaded(t, r); got.RecVersion != 2 {
		t.Fatalf("a rejected save must not change the record: version=%d, want 2", got.RecVersion)
	}
}

func testSaveWithoutIDRejected(t *testing.T, r repository.Repository) {
	bad := rec("")
	if err := r.Save(context.Background(), bad); err == nil {
		t.Fatal("an ID-less record must be rejected")
	}
}

func testSaveWithoutGroupRejected(t *testing.T, r repository.Repository) {
	bad := rec("i1")
	bad.Group = ""

	if err := r.Save(context.Background(), bad); err == nil {
		t.Fatal("a group-less record must be rejected (SRD-078 FR-1)")
	}
}

func testSaveUnregisteredGroupRejected(t *testing.T, r repository.Repository) {
	if err := r.Save(context.Background(), rec("i1")); err == nil {
		t.Fatal("a record referencing an unestablished group must be rejected (SRD-078 FR-1)")
	}
}

func testGroupRegistryIdempotent(t *testing.T, r repository.Repository) {
	ctx := context.Background()

	for range 2 { // establishing an existing group is a no-op
		if err := r.RegisterGroup(ctx, "conformance-group"); err != nil {
			t.Fatalf("RegisterGroup: %v", err)
		}
	}

	ok, err := r.GroupExists(ctx, "conformance-group")
	if err != nil || !ok {
		t.Fatalf("GroupExists after register = %v/%v, want true/nil", ok, err)
	}
}

func testGroupExistsUnknown(t *testing.T, r repository.Repository) {
	ok, err := r.GroupExists(context.Background(), "never-registered")
	if err != nil || ok {
		t.Fatalf("GroupExists(unknown) = %v/%v, want false/nil", ok, err)
	}
}

func testRegistryEmptyGroupRejected(t *testing.T, r repository.Repository) {
	ctx := context.Background()

	if err := r.RegisterGroup(ctx, ""); err == nil {
		t.Fatal("RegisterGroup must reject an empty group")
	}

	if _, err := r.GroupExists(ctx, ""); err == nil {
		t.Fatal("GroupExists must reject an empty group")
	}
}

func testLoadFidelity(t *testing.T, r repository.Repository) {
	in := rec("i1")
	in.Tenant = "acme"
	in.Lease = repository.Lease{
		Owner:       "engine-a",
		Incarnation: 3,
		Expiry:      now.Add(30 * time.Second),
	}

	mustSave(t, r, in)

	got := loaded(t, r)
	if !bytes.Equal(got.Payload, in.Payload) {
		t.Fatalf("payload = %q, want %q", got.Payload, in.Payload)
	}

	if got.Group != in.Group || got.Tenant != in.Tenant {
		t.Fatalf("partitions = %q/%q, want %q/%q", got.Group, got.Tenant, in.Group, in.Tenant)
	}

	if got.Lease.Owner != in.Lease.Owner ||
		got.Lease.Incarnation != in.Lease.Incarnation ||
		!got.Lease.Expiry.Equal(in.Lease.Expiry) {
		t.Fatalf("lease = %+v, want %+v", got.Lease, in.Lease)
	}

	if got.Status != in.Status {
		t.Fatalf("status = %v, want %v", got.Status, in.Status)
	}
}

func testLoadAbsent(t *testing.T, r repository.Repository) {
	_, ok, err := r.Load(context.Background(), "missing")
	if err != nil || ok {
		t.Fatalf("loading an absent id: ok=%v err=%v, want false/nil", ok, err)
	}
}

func testPayloadIsolation(t *testing.T, r repository.Repository) {
	in := rec("i1")
	mustSave(t, r, in)

	in.Payload[0] = 'X' // mutating the saved slice must not reach the store
	if got := loaded(t, r); got.Payload[0] == 'X' {
		t.Fatal("the store shares memory with the saved record's payload")
	}

	got := loaded(t, r)
	got.Payload[0] = 'Y' // mutating a loaded slice must not reach the store
	if again := loaded(t, r); again.Payload[0] == 'Y' {
		t.Fatal("the store shares memory with a loaded record's payload")
	}
}

func testDeleteIdempotent(t *testing.T, r repository.Repository) {
	ctx := context.Background()

	mustSave(t, r, rec("i1"))

	if err := r.Delete(ctx, "i1"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if _, ok, err := r.Load(ctx, "i1"); err != nil || ok {
		t.Fatalf("the record survived its delete: ok=%v err=%v", ok, err)
	}

	if err := r.Delete(ctx, "i1"); err != nil {
		t.Fatalf("deleting an absent record must be a no-op, got %v", err)
	}
}

// testListFilters proves the claimability filters one by one: terminal
// and suspended records never list; a live lease hides a record; an
// expired lease exposes it.
func testListFilters(t *testing.T, r repository.Repository) {
	ctx := context.Background()

	saveVariant := func(id string, mut func(*repository.InstanceRecord)) {
		v := rec(id)
		mut(&v)
		mustSave(t, r, v)
	}

	saveVariant("claimable", func(*repository.InstanceRecord) {})
	saveVariant("completed", func(v *repository.InstanceRecord) {
		v.Status = repository.StatusCompleted
	})
	saveVariant("terminated", func(v *repository.InstanceRecord) {
		v.Status = repository.StatusTerminated
	})
	saveVariant("suspended", func(v *repository.InstanceRecord) {
		v.Status = repository.StatusSuspended
	})
	saveVariant("leased", func(v *repository.InstanceRecord) {
		v.Lease = repository.Lease{
			Owner: "engine-b", Incarnation: 1, Expiry: now.Add(time.Hour),
		}
	})
	saveVariant("lease-lapsed", func(v *repository.InstanceRecord) {
		v.Lease = repository.Lease{
			Owner: "engine-b", Incarnation: 1, Expiry: now.Add(-time.Second),
		}
	})

	ids, err := r.ListInFlight(ctx, "conformance-group", now)
	if err != nil {
		t.Fatalf("ListInFlight: %v", err)
	}

	want := []string{"claimable", "lease-lapsed"}
	if !slices.Equal(ids, want) {
		t.Fatalf("ListInFlight = %v, want %v", ids, want)
	}
}

func testListGroupScoped(t *testing.T, r repository.Repository) {
	ctx := context.Background()

	a := rec("a1")
	a.Group = "group-a"
	b := rec("b1")
	b.Group = "group-b"
	mustSave(t, r, a)
	mustSave(t, r, b)

	ids, err := r.ListInFlight(ctx, "group-a", now)
	if err != nil {
		t.Fatalf("ListInFlight(group-a): %v", err)
	}

	if !slices.Equal(ids, []string{"a1"}) {
		t.Fatalf("group-a listing = %v, want [a1] only — groups must never cross-list", ids)
	}
}

func testListEmptyGroupRejected(t *testing.T, r repository.Repository) {
	if _, err := r.ListInFlight(context.Background(), "", now); err == nil {
		t.Fatal("an empty group must be rejected (SRD-078 FR-1)")
	}
}

func testListUnregisteredGroupEmpty(t *testing.T, r repository.Repository) {
	ids, err := r.ListInFlight(context.Background(), "never-registered", now)
	if err != nil || len(ids) != 0 {
		t.Fatalf("an unregistered group must list empty without error, got %v/%v", ids, err)
	}
}

func testListDeterministicOrder(t *testing.T, r repository.Repository) {
	ctx := context.Background()

	for _, id := range []string{"c", "a", "b"} {
		mustSave(t, r, rec(id))
	}

	first, err := r.ListInFlight(ctx, "conformance-group", now)
	if err != nil {
		t.Fatalf("ListInFlight: %v", err)
	}

	second, err := r.ListInFlight(ctx, "conformance-group", now)
	if err != nil {
		t.Fatalf("second ListInFlight: %v", err)
	}

	if !slices.Equal(first, []string{"a", "b", "c"}) ||
		!slices.Equal(second, first) {
		t.Fatalf("listing must be deterministically ordered: %v then %v", first, second)
	}
}

// mustRegister establishes the baseline conformance group.
func mustRegister(t *testing.T, r repository.Repository) {
	t.Helper()

	if err := r.RegisterGroup(context.Background(), "conformance-group"); err != nil {
		t.Fatalf("register the conformance group: %v", err)
	}
}

// mustSave establishes the record's group (idempotently) and saves —
// the normal engine sequence (the engine registers its group at Run).
func mustSave(t *testing.T, r repository.Repository, v repository.InstanceRecord) {
	t.Helper()

	if err := r.RegisterGroup(context.Background(), v.Group); err != nil {
		t.Fatalf("register group %q: %v", v.Group, err)
	}

	if err := r.Save(context.Background(), v); err != nil {
		t.Fatalf("save %q: %v", v.ID, err)
	}
}

func loaded(t *testing.T, r repository.Repository) repository.InstanceRecord {
	t.Helper()

	got, ok, err := r.Load(context.Background(), "i1")
	if err != nil || !ok {
		t.Fatalf("load i1: ok=%v err=%v", ok, err)
	}

	return got
}

func wantConcurrentUpdate(t *testing.T, err error) {
	t.Helper()

	var ae *errs.ApplicationError
	if !errors.As(err, &ae) || !ae.HasClass(errs.ConcurrentUpdate) {
		t.Fatalf("want a ConcurrentUpdate-classified error, got %v", err)
	}
}
