// Package adapters lets a host's own Go struct participate directly as a
// navigable process value (ADR-011 v.6 §2.9.5, SRD-045): Wrap(&order) returns
// a live data.Record view — wrap, not convert — that every data seam (path
// walks, SetPath, DiffValues, conditions, mappings) consumes through the
// ordinary Record/Collection capabilities, with zero engine change.
//
// How a type answers the capabilities is a per-type structural adapter,
// resolved once through the type→adapter registry (the encoding/json
// type-cache pattern) and cached. This package is the ONE place the engine's
// anti-reflection stance is deliberately relaxed, and the relaxation is
// bounded: reflection walks a type once, at the first Wrap of that type, off
// the execution path; field access thereafter is a cached-index accessor
// call. Custom types plug in via Register (the Marshaler-analog seam) or by
// implementing data.Value themselves (the passthrough kind).
//
// Concurrency invariant: after Wrap, access the value through the adapter
// (guarded by the root mutex). A host mutating the wrapped struct directly,
// concurrently with process evaluation, owns that synchronization itself —
// the same live-value posture values.Array.GetP takes.
package adapters

// errorClass identifies this package in classified errors.
const errorClass = "ADAPTERS_ERROR"
