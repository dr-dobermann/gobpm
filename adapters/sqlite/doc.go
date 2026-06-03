// Package sqlite will provide a SQLite-backed Repository implementation
// for goBpm per ADR-002 §4.2 and ADR-003 §4.2.
//
// Status: scaffold only at SRD-001 landing time. This module exists to
// validate the adapters/* multi-module pattern; the substantive
// Repository implementation lands in a subsequent SRD.
//
// When implemented, this adapter will:
//   - Implement pkg/repository.Repository (per ADR-003 §4.2).
//   - Use a pure-Go SQLite driver (default: modernc.org/sqlite) so the
//     core path stays CGo-free; CGo-driver alternative may be added behind
//     a build tag if needed.
//   - Be wired by the runtime via thresher.WithRepository(...) options.
//   - Pass the published Repository conformance test suite once the
//     conformance helper exists (per ADR-003 §4.2).
//   - Declare itself NOT cluster-safe via the ClusterAware optional
//     interface (per ADR-002 §8.3 and SAD-001 §13.5): single-file
//     SQLite cannot honor cluster semantics. Operators wanting cluster
//     mode select a different Repository adapter (e.g., postgres).
package sqlite
