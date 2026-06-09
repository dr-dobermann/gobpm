package memmetrics

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/observability"
)

func attr(k string, v any) observability.Attr { return observability.Attr{Key: k, Value: v} }

type capLogger struct{ warns int }

func (l *capLogger) Debug(string, ...any) {}
func (l *capLogger) Info(string, ...any)  {}
func (l *capLogger) Warn(string, ...any)  { l.warns++ }
func (l *capLogger) Error(string, ...any) {}

func TestCounterAccumulatesPerSeries(t *testing.T) {
	r := New()
	ctx := context.Background()

	c := r.Counter("hits")
	c.Add(ctx, 1)
	c.Add(ctx, 2)
	c.Add(ctx, 5, attr("path", "/a"))

	snap := r.Snapshot()
	if got := snap.Counters["hits"][""]; got != 3 {
		t.Fatalf("no-attr counter = %v, want 3", got)
	}

	if got := snap.Counters["hits"]["path=/a"]; got != 5 {
		t.Fatalf("attr counter = %v, want 5", got)
	}
}

func TestInstrumentsAreReused(t *testing.T) {
	r := New()
	if r.Counter("x") != r.Counter("x") {
		t.Fatal("Counter must return the same instrument for the same name")
	}

	if r.Gauge("x") != r.Gauge("x") {
		t.Fatal("Gauge must return the same instrument for the same name")
	}

	if r.Histogram("x") != r.Histogram("x") {
		t.Fatal("Histogram must return the same instrument for the same name")
	}
}

func TestGaugeKeepsLastValue(t *testing.T) {
	r := New()
	ctx := context.Background()

	g := r.Gauge("temp")
	g.Set(ctx, 1)
	g.Set(ctx, 9)

	if got := r.Snapshot().Gauges["temp"][""]; got != 9 {
		t.Fatalf("gauge = %v, want 9", got)
	}
}

func TestHistogramDistribution(t *testing.T) {
	r := New(WithBuckets([]float64{1, 5, 10}))
	ctx := context.Background()

	h := r.Histogram("lat")
	h.Record(ctx, 0.5)
	h.Record(ctx, 3)
	h.Record(ctx, 7)

	hd := r.Snapshot().Histograms["lat"][""]
	if hd.Count != 3 {
		t.Fatalf("count = %d, want 3", hd.Count)
	}

	if hd.Sum != 10.5 {
		t.Fatalf("sum = %v, want 10.5", hd.Sum)
	}

	// Cumulative counts for bounds [1,5,10]: <=1 -> {0.5}=1; <=5 -> {0.5,3}=2;
	// <=10 -> all 3.
	for i, want := range []uint64{1, 2, 3} {
		if hd.Buckets[i] != want {
			t.Fatalf("bucket[%d] = %d, want %d", i, hd.Buckets[i], want)
		}
	}
}

func TestSeriesCapDropsAndWarnsOnce(t *testing.T) {
	lg := &capLogger{}
	r := New(WithMaxSeries(2), WithLogger(lg))
	ctx := context.Background()

	c := r.Counter("c")
	c.Add(ctx, 1, attr("id", "1")) // series 1
	c.Add(ctx, 1, attr("id", "2")) // series 2 (cap reached)
	c.Add(ctx, 1, attr("id", "3")) // dropped
	c.Add(ctx, 1, attr("id", "3")) // dropped again

	// Other instrument kinds are dropped too once the cap is hit.
	r.Gauge("g").Set(ctx, 1, attr("id", "x"))
	r.Histogram("h").Record(ctx, 1, attr("id", "y"))

	snap := r.Snapshot()
	if len(snap.Counters["c"]) != 2 {
		t.Fatalf("counter series = %d, want 2", len(snap.Counters["c"]))
	}

	if _, ok := snap.Counters["c"]["id=3"]; ok {
		t.Fatal("series id=3 should have been dropped")
	}

	if len(snap.Gauges["g"]) != 0 {
		t.Fatal("gauge series should have been dropped past the cap")
	}

	if len(snap.Histograms["h"]) != 0 {
		t.Fatal("histogram series should have been dropped past the cap")
	}

	if lg.warns != 1 {
		t.Fatalf("warns = %d, want exactly 1 (warn once)", lg.warns)
	}
}

func TestSeriesCapDisabled(t *testing.T) {
	r := New(WithMaxSeries(0))
	ctx := context.Background()

	c := r.Counter("c")
	for i := range 5 {
		c.Add(ctx, 1, attr("id", i))
	}

	if got := len(r.Snapshot().Counters["c"]); got != 5 {
		t.Fatalf("series = %d, want 5 (cap disabled)", got)
	}
}

func TestSeriesKeyIsOrderIndependent(t *testing.T) {
	a := seriesKey([]observability.Attr{attr("b", 2), attr("a", 1)})
	b := seriesKey([]observability.Attr{attr("a", 1), attr("b", 2)})

	if a != b {
		t.Fatalf("attribute order changed the key: %q vs %q", a, b)
	}

	if a != "a=1,b=2" {
		t.Fatalf("key = %q, want %q", a, "a=1,b=2")
	}

	if seriesKey(nil) != "" {
		t.Fatal("empty attribute set must map to the empty key")
	}
}

func TestSnapshotIsIsolated(t *testing.T) {
	r := New()
	ctx := context.Background()

	r.Counter("c").Add(ctx, 1)
	snap := r.Snapshot()
	r.Counter("c").Add(ctx, 1)

	if snap.Counters["c"][""] != 1 {
		t.Fatal("snapshot must not observe mutations made after it was taken")
	}
}
