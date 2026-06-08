// Package memmetrics provides the engine's default MetricsRecorder: an
// in-memory, queryable registry. It keeps current counter sums, last gauge
// values, and histogram distributions in memory, readable via Snapshot for
// diagnostics and tests. To bound memory under high-cardinality attribute sets
// it caps the total number of series, dropping new series past the cap and
// logging a single warning (ADR-002 §4.2).
package memmetrics

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// DefaultMaxSeries is the default cap on distinct series (instrument × attribute
// set) the registry retains.
const DefaultMaxSeries = 1024

// DefaultBuckets is the default set of histogram upper bounds (seconds-oriented,
// matching the ADR-002 §8.5 latency histograms).
var DefaultBuckets = []float64{
	.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10,
}

// Registry is an in-memory observability.MetricsRecorder.
type Registry struct {
	logger    observability.Logger
	counters  map[string]*counter
	histos    map[string]*histogram
	gauges    map[string]*gauge
	buckets   []float64
	maxSeries int
	series    int
	mu        sync.Mutex
	warnOnce  sync.Once
}

// Option configures a Registry.
type Option func(*Registry)

// WithMaxSeries sets the cap on distinct series; n <= 0 disables the cap.
func WithMaxSeries(n int) Option { return func(r *Registry) { r.maxSeries = n } }

// WithLogger sets the logger used for the series-cap warning.
func WithLogger(l observability.Logger) Option { return func(r *Registry) { r.logger = l } }

// WithBuckets sets the histogram upper bounds.
func WithBuckets(bounds []float64) Option {
	return func(r *Registry) { r.buckets = append([]float64(nil), bounds...) }
}

// New returns an in-memory Registry with the default cap, buckets, and
// slog.Default() logger, overridden by opts.
func New(opts ...Option) *Registry {
	r := &Registry{
		logger:    slog.Default(),
		buckets:   DefaultBuckets,
		counters:  map[string]*counter{},
		histos:    map[string]*histogram{},
		gauges:    map[string]*gauge{},
		maxSeries: DefaultMaxSeries,
	}

	for _, o := range opts {
		o(r)
	}

	return r
}

// Counter returns (creating if needed) the named counter instrument.
func (r *Registry) Counter(name string) observability.Counter {
	r.mu.Lock()
	defer r.mu.Unlock()

	c, ok := r.counters[name]
	if !ok {
		c = &counter{reg: r, vals: map[string]float64{}}
		r.counters[name] = c
	}

	return c
}

// Histogram returns (creating if needed) the named histogram instrument.
func (r *Registry) Histogram(name string) observability.Histogram {
	r.mu.Lock()
	defer r.mu.Unlock()

	h, ok := r.histos[name]
	if !ok {
		h = &histogram{reg: r, series: map[string]*histData{}}
		r.histos[name] = h
	}

	return h
}

// Gauge returns (creating if needed) the named gauge instrument.
func (r *Registry) Gauge(name string) observability.Gauge {
	r.mu.Lock()
	defer r.mu.Unlock()

	g, ok := r.gauges[name]
	if !ok {
		g = &gauge{reg: r, vals: map[string]float64{}}
		r.gauges[name] = g
	}

	return g
}

// admit reports whether a new series may be recorded, accounting it against the
// cap. The caller must hold r.mu.
func (r *Registry) admit() bool {
	if r.maxSeries > 0 && r.series >= r.maxSeries {
		r.warnOnce.Do(func() {
			r.logger.Warn("memmetrics: series cap reached, dropping new series",
				"cap", r.maxSeries)
		})

		return false
	}

	r.series++

	return true
}

type counter struct {
	reg  *Registry
	vals map[string]float64
}

func (c *counter) Add(_ context.Context, value float64, attrs ...observability.Attr) {
	key := seriesKey(attrs)

	c.reg.mu.Lock()
	defer c.reg.mu.Unlock()

	if _, ok := c.vals[key]; !ok && !c.reg.admit() {
		return
	}

	c.vals[key] += value
}

type gauge struct {
	reg  *Registry
	vals map[string]float64
}

func (g *gauge) Set(_ context.Context, value float64, attrs ...observability.Attr) {
	key := seriesKey(attrs)

	g.reg.mu.Lock()
	defer g.reg.mu.Unlock()

	if _, ok := g.vals[key]; !ok && !g.reg.admit() {
		return
	}

	g.vals[key] = value
}

type histogram struct {
	reg    *Registry
	series map[string]*histData
}

type histData struct {
	buckets []uint64
	sum     float64
	count   uint64
}

func (h *histogram) Record(_ context.Context, value float64, attrs ...observability.Attr) {
	key := seriesKey(attrs)

	h.reg.mu.Lock()
	defer h.reg.mu.Unlock()

	d, ok := h.series[key]
	if !ok {
		if !h.reg.admit() {
			return
		}

		d = &histData{buckets: make([]uint64, len(h.reg.buckets))}
		h.series[key] = d
	}

	d.count++
	d.sum += value

	for i, ub := range h.reg.buckets {
		if value <= ub {
			d.buckets[i]++
		}
	}
}

// seriesKey renders an attribute set into a stable, order-independent key.
func seriesKey(attrs []observability.Attr) string {
	if len(attrs) == 0 {
		return ""
	}

	sorted := make([]observability.Attr, len(attrs))
	copy(sorted, attrs)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Key < sorted[j].Key })

	parts := make([]string, len(sorted))
	for i, a := range sorted {
		parts[i] = fmt.Sprintf("%s=%v", a.Key, a.Value)
	}

	return strings.Join(parts, ",")
}

var _ observability.MetricsRecorder = (*Registry)(nil)
