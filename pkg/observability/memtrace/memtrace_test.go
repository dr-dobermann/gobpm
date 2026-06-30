package memtrace

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// TestLiveSpanConcurrentUse covers FIX-014 1.11: a span's mutating methods are
// safe under concurrent use (run with -race). Without the per-span mutex this
// races on the span's data/ended fields.
func TestLiveSpanConcurrentUse(t *testing.T) {
	r := New(8)
	_, span := r.Start(context.Background(), "concurrent")

	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()

			span.SetAttributes(observability.Attr{Key: "k", Value: i})
			span.SetStatus(observability.StatusOK, strconv.Itoa(i))
			span.RecordError(errors.New("boom"))
		}(i)
	}

	wg.Wait()
	span.End()
}

func TestRecordsCompletedSpan(t *testing.T) {
	r := New(0) // default capacity

	_, sp := r.Start(context.Background(), "task", observability.Attr{Key: "id", Value: 1})
	sp.SetAttributes(observability.Attr{Key: "x", Value: "y"})
	sp.RecordError(errors.New("boom"))
	sp.SetStatus(observability.StatusError, "failed")
	sp.End()
	sp.End() // idempotent — must not record twice

	spans := r.Spans()
	if len(spans) != 1 {
		t.Fatalf("spans = %d, want 1", len(spans))
	}

	got := spans[0]
	if got.Name != "task" {
		t.Fatalf("name = %q, want task", got.Name)
	}

	if got.Status != observability.StatusError || got.StatusDesc != "failed" {
		t.Fatalf("status = %v/%q", got.Status, got.StatusDesc)
	}

	if got.Err == nil {
		t.Fatal("error was not recorded")
	}

	if len(got.Attrs) != 2 {
		t.Fatalf("attrs = %d, want 2 (initial + added)", len(got.Attrs))
	}
}

func TestRingEvictsOldest(t *testing.T) {
	r := New(2)

	for i := range 3 {
		_, sp := r.Start(context.Background(), strconv.Itoa(i))
		sp.End()
	}

	spans := r.Spans()
	if len(spans) != 2 {
		t.Fatalf("spans = %d, want 2", len(spans))
	}

	if spans[0].Name != "1" || spans[1].Name != "2" {
		t.Fatalf("retained = %q,%q; want 1,2", spans[0].Name, spans[1].Name)
	}
}
