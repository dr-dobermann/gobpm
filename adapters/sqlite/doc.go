// Package sqlite will provide a SQLite-backed Repository implementation
// for goBpm per ADR-002 §4.2 and ADR-003 §4.2.
//
// Status: SCAFFOLD ONLY — this package contains no implementation and is not
// usable. It exists to validate the adapters/* multi-module pattern, and it
// has held that position since SRD-001 without a line of code behind it.
//
// The implementation is tracked as issue #316:
// https://github.com/dr-dobermann/gobpm/issues/316
//
// Saying so plainly matters more than it looks: a doc comment written in the
// future tense reads as work in progress, and this one has read that way for
// long enough that ADR-002 §4.2 and ADR-003 §4.2 both name SQLite as though a
// user could reach for it. Anyone who needs a durable Repository today wants
// adapters/postgres or their own; what follows is a plan, not a promise about
// when.
//
// When implemented, this adapter will:
//   - Implement pkg/repository.Repository (per ADR-003 §4.2).
//   - Use a pure-Go SQLite driver (default: modernc.org/sqlite) so the
//     core path stays CGo-free; CGo-driver alternative may be added behind
//     a build tag if needed.
//   - Be wired by the runtime via thresher.WithRepository(...) options.
//   - Pass the published Repository conformance suite, which now exists:
//     repositorytest.Conformance (per ADR-003 §4.2).
//   - Declare itself NOT cluster-safe via the ClusterAware optional
//     interface (per ADR-002 §8.3 and SAD-001 §13.5): single-file
//     SQLite cannot honor cluster semantics. Operators wanting cluster
//     mode select a different Repository adapter (e.g., postgres).
package sqlite
