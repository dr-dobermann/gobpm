// Package runtime is the standalone-server wrapper around goBpm core.
//
// Responsibilities per ADR-004 (docs/design/ADR-004-runtime-environment-contract.md):
// multi-tenant context extraction, AuthN integration, observability adapter
// wiring, REST + gRPC API surfaces, diagnostic endpoints, health checks, and
// the engine lifecycle (startup ordering, graceful drain on shutdown).
//
// This module is NOT the embedded library. For embedded use of goBpm,
// import github.com/dr-dobermann/gobpm directly.
//
// Implementation is in progress; SRDs against ADR-003 §4.6 and the
// ADR-004 service groups fill in the substantive code over time. At the
// time of SRD-001 landing, this module contains only this package
// documentation plus a stub cmd/gobpm-server/main.go binary that prints
// a placeholder message and exits.
package runtime
