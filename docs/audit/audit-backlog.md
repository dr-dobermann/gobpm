# Audit backlog — findings that need design work, not a FIX

Findings surfaced by the code-review audits whose remediation exceeds a one-shot
FIX — they change *semantics* or a *contract*, so they need an ADR update and/or
a dedicated SRD rather than a defect patch. Parked here for future research and
development; each entry names the governing docs to amend. A FIX-track item, by
contrast, lands as `docs/fix/FIX-NNN`.

---

## AB-001 — Keyless `ParallelEvents` start gate double-instantiates

- **Source**: `docs/audit/code-review-third-pass-2026-06-29.md` §2.5 (🟠 P2,
  Active for the keyless configuration).
- **Code**: `pkg/thresher/instance_starter.go:152-158`,
  `pkg/thresher/thresher.go` `resolveAndLaunch`,
  `pkg/model/gateways/event_based.go:493-514`, `validateStartGate`.

**Problem.** An event-based gateway used as a process start in `ParallelEvents`
mode must produce **one** instance that completes when **all** arms' messages
have arrived (SRD-025 §4.3). Each arm is a persistent subscription, and the
create-or-route decision is keyed on a **correlation key** (ADR-016): the first
message mints an instance, later same-key messages route into it. If the gate
declares **no** `CorrelationKey`, `deriveKey` yields `""` and `resolveAndLaunch`
takes the no-dedup branch that **always** creates a new instance — so every arm
message spawns its own instance, each firing only its one arm and waiting forever
for the others (whose messages went to sibling instances). N arms → N stuck
instances, none completing. `Process.Validate` currently lets the keyless gate
through, and every test supplies a key, so the broken case is untested.

**Why it's not a FIX.** The remediation is a *semantics decision*, not a defect
patch: what should a keyless parallel-start gate **mean**? Two directions, each
with contract consequences:

1. **Reject at validation** — a `ParallelEvents` instantiating gate with no
   `CorrelationKey` fails `Process.Validate`. Standard-conformant, fail-fast;
   makes the keyless config illegal. Changes the validation contract.
2. **Key the dedup on the gate id** when no correlation key is present — all
   arms of one gate route into a single instance by construction. Changes the
   instantiation/correlation model (a gate-scoped implicit key).

Choosing between them — and pinning the BPMN-conformance argument — is ADR
work, and landing it (validation rule or implicit-key derivation + tests for the
keyless path) is a dedicated SRD.

**Governing docs to amend.**
- **ADR-015** (event-triggered instantiation) — the instantiation decision for a
  keyless parallel-start gate.
- **ADR-016** (message correlation) — if direction 2 is taken (implicit gate-id
  key when no `CorrelationKey`).
- **SRD-025** (event-based-gateway instantiation) — update §4.3 for the chosen
  keyless semantics; the landing SRD references it.

**Status**: Parked (was tentatively reserved as "FIX-014"; reclassified as
design work 2026-06-30).
