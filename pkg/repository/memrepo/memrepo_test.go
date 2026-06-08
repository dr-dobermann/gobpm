package memrepo

import (
	"context"
	"strconv"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/repository"
)

type capLogger struct{ warns int }

func (l *capLogger) Debug(string, ...any) {}
func (l *capLogger) Info(string, ...any)  {}
func (l *capLogger) Warn(string, ...any)  { l.warns++ }
func (l *capLogger) Error(string, ...any) {}

func rec(id string, st repository.Status) repository.InstanceRecord {
	return repository.InstanceRecord{State: id, ID: id, Status: st}
}

func TestSaveLoadDelete(t *testing.T) {
	r := New()
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
	r := New()
	ctx := context.Background()

	_ = r.Save(ctx, rec("b", repository.StatusActive))
	_ = r.Save(ctx, rec("a", repository.StatusActive))
	_ = r.Save(ctx, rec("c", repository.StatusCompleted))

	ids, _ := r.ListInFlight(ctx)
	if len(ids) != 2 || ids[0] != "a" || ids[1] != "b" {
		t.Fatalf("in-flight = %v, want [a b]", ids)
	}
}

func TestActiveNeverEvictedTerminalCapped(t *testing.T) {
	lg := &capLogger{}
	r := New(WithMaxTerminal(2), WithLogger(lg))
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
	r := New(WithMaxTerminal(1))
	ctx := context.Background()

	_ = r.Save(ctx, rec("t1", repository.StatusCompleted))
	_ = r.Save(ctx, rec("t1", repository.StatusCompleted)) // re-save: still one series
	_ = r.Save(ctx, rec("t2", repository.StatusCompleted)) // now two -> evict t1

	if _, ok, _ := r.Load(ctx, "t1"); ok {
		t.Fatal("t1 should be evicted once t2 is saved")
	}

	if _, ok, _ := r.Load(ctx, "t2"); !ok {
		t.Fatal("t2 should be retained")
	}
}

func TestDeleteTerminalUntracks(t *testing.T) {
	r := New(WithMaxTerminal(2))
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
	r := New(WithMaxTerminal(0))
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
