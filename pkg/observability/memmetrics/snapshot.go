package memmetrics

import "maps"

// Snapshot is an immutable copy of the registry's current values, intended for
// diagnostics and test assertions. Series are keyed by their attribute-set
// rendering (see seriesKey); the empty string is the no-attribute series.
type Snapshot struct {
	Counters   map[string]map[string]float64
	Gauges     map[string]map[string]float64
	Histograms map[string]map[string]HistogramData
}

// HistogramData is one histogram series' accumulated distribution.
type HistogramData struct {
	Bounds  []float64
	Buckets []uint64
	Sum     float64
	Count   uint64
}

// Snapshot returns a deep copy of the registry's current values.
func (r *Registry) Snapshot() Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()

	s := Snapshot{
		Counters:   make(map[string]map[string]float64, len(r.counters)),
		Gauges:     make(map[string]map[string]float64, len(r.gauges)),
		Histograms: make(map[string]map[string]HistogramData, len(r.histos)),
	}

	for name, c := range r.counters {
		s.Counters[name] = copyFloatMap(c.vals)
	}

	for name, g := range r.gauges {
		s.Gauges[name] = copyFloatMap(g.vals)
	}

	for name, h := range r.histos {
		m := make(map[string]HistogramData, len(h.series))
		for key, d := range h.series {
			m[key] = HistogramData{
				Bounds:  append([]float64(nil), r.buckets...),
				Buckets: append([]uint64(nil), d.buckets...),
				Sum:     d.sum,
				Count:   d.count,
			}
		}

		s.Histograms[name] = m
	}

	return s
}

func copyFloatMap(src map[string]float64) map[string]float64 {
	dst := make(map[string]float64, len(src))
	maps.Copy(dst, src)

	return dst
}
