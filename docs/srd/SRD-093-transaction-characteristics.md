# SRD-093 — Transaction characteristics: model, bind, registration check, BPMN import

| Field | Value |
|---|---|
| Status | Draft |
| Date | 2026-08-26 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-028 v.2](../design/ADR-028-transaction-sub-process.md) §2.1 (characteristics composed into the Sub-Process; the bind step), §2.7 (open `method`, carried `protocol`, refusal at registration), §2.8 (the seam stays deferred) |
| Upstream | [ADR-024](../design/ADR-024-process-interchange-converters.md) §2.16 (a converter keeps no second copy of a model rule), §7 (export waits on slice 3); [ADR-025](../design/ADR-025-activity-iteration-loop-and-multi-instance.md) §2.1 (the characteristics shape); [ADR-026](../design/ADR-026-compensation-events.md) §2.2/§2.4 (the ledger the built-in coordinator acts on); [ADR-002](../design/ADR-002-extension-architecture.md) (registration-time coverage checks) |
| Related | [SRD-061](SRD-061-transaction-sub-process.md) (the v.1 landing this reshapes), [SRD-089.E](SRD-089.E-bpmn-import-containers-and-lanes.md) §4.5 (the import dispositions this supersedes — frozen, not retro-edited) |
| Part of | [#324](https://github.com/dr-dobermann/gobpm/issues/324) — the model, bind and import half; export rides ADR-024 slice 3 |

## §1 Background

ADR-028 v.1 landed the Transaction as a bare boolean: `subProcessConfig.isTransaction`
(`pkg/model/activities/subprocess_options.go:14`) set by a zero-argument
`WithTransaction()` (`:58`), copied into `SubProcess.isTransaction`
(`subprocess.go:63`, `:123`, cloned at `:639`) and read through
`IsTransaction()` (`:193`). Three consumers duck-type that method:
`validateTransactionRules` (`subprocess.go:397`, no nested Transaction), the
ad-hoc exclusion (`adhoc_validation.go:69`) and the Cancel-boundary host check
(`pkg/model/events/boundary.go:116`). The Transaction's two own attributes have
no field anywhere.

The converter fills the gap with a private rule. `transactionOptions`
(`pkg/convert/bpmn/dispatch.go:519`) reads `method` against a package table
`transactionMethods` (`:499`) keyed `""`, `compensate`, `store`, `image` — the
metamodel spellings only. The schema's own default token `##Compensate` is not
a key, so a schema-valid `<transaction method="##Compensate">` is refused as
*"not one of BPMN's compensate, store or image"* (`:527`). `protocol` is read
and reported as `Dropped` (`:543`), and the process is built with the bare
marker. `TestTransactionMethodDispositions` (`variants_test.go:131`) and
`TestTransactionProtocolIsReported` (`:248`) pin that behaviour;
`TestRefusalsSayWhichKindTheyAre` (`refusalwording_test.go:33`) pins the
`store` refusal's wording as a standing boundary.

At run time the abort has one owner, hard-wired: `cancelTransaction`
(`internal/instance/transaction.go:25`) runs the ADR-026 sweep over the
scope's ledger and `finalizeTransaction` (`:64`) tears the scope down. The
ledger itself is fed by every inner node's completion (`compensation_ledger.go:92`,
`:126`) — the "inner nodes report to the coordinator" that ADR-028 v.2 §2.1
names, already true for compensate. Registration-time coverage checks exist
for one host seam: `validateScriptCoverage` (`pkg/thresher/script_validation.go:30`)
walks the snapshot and refuses a Script Task whose format no engine claims,
called from `RegisterProcess` (`thresher.go:1041`).

Export writes no Sub-Process of any kind: `nodeXML` (`pkg/convert/bpmn/exporter.go:333`)
maps start/end, task/user/service, exclusive/parallel and refuses the rest.
ADR-024 §7's *slice 3 — export catches up* is still open, so re-emitting the
two attributes is out of this document's reach and is said so.

## §2 Requirements

### Functional

**FR-1 — A `TransactionCharacteristics` value object.** `pkg/model/activities`
gains

```go
// TransactionMethod names the coordinator that aborts a Transaction
// (ADR-028 §2.7). Open: the schema's tTransactionMethod admits any URI.
type TransactionMethod string

// TransactionCompensate is the built-in coordinator and the default.
const TransactionCompensate TransactionMethod = "compensate"

// ParseTransactionMethod reads a document's method attribute: "" (absent),
// "compensate" and the schema token "##Compensate" all denote
// TransactionCompensate; any other non-blank value is carried as is.
func ParseTransactionMethod(s string) TransactionMethod

type TransactionCharacteristics struct { method TransactionMethod; protocol string }
func (tc *TransactionCharacteristics) Method() TransactionMethod
func (tc *TransactionCharacteristics) Protocol() string
```

Immutable after construction; shared by clones like the ad-hoc spec.

**FR-2 — `WithTransaction` takes transaction options.** The signature becomes
`WithTransaction(opts ...TransactionOption) SubProcessOption`, so every
existing zero-argument call keeps compiling and means a compensate
transaction. Two options: `WithTransactionMethod(m TransactionMethod)` refuses
a blank method; `WithTransactionProtocol(p string)` refuses a blank protocol.
Both errors name the option and the parameter. `protocol` cannot be set on a
non-Transaction Sub-Process by construction — the option exists only inside
`WithTransaction` — which realizes ADR-028 §2.7's "construction error" as a
shape rather than a runtime check.

**FR-3 — The Sub-Process carries the object and derives the flag.**
`SubProcess.isTransaction bool` becomes `tx *TransactionCharacteristics`;
`Transaction() *TransactionCharacteristics` returns it (nil on a plain or
Event Sub-Process); `IsTransaction()` returns `sp.tx != nil`. The three
duck-typed consumers, the two exclusivity checks in `NewSubProcess`, and the
clone are unchanged in behaviour.

**FR-4 — Registration refuses a method with no coordinator.** `RegisterProcess`
runs `validateTransactionCoverage(s)` beside the script check: a deep walk;
every Transaction whose method is not `TransactionCompensate` is collected
and the process is refused with one error naming each offender (`name
(method %q)`), the fact that the engine coordinates *compensate only* (ADR-028
§2.7), and the alternative — model the undo as compensation handlers. Sorted,
like the script check, so the message is stable.

**FR-5 — The executing unit binds the scope to its coordinator.** When the
loop opens a Transaction's child scope, the scope entry records the binding —
the characteristics' method and protocol — and `cancelTransaction` reads the
binding rather than assuming: the compensate binding runs the existing sweep;
any other binding is an invariant violation (registration guarantees it
cannot occur) and is reported as such rather than silently compensating. The
thrown-compensation fact gains the bound method in its details
(`observability.AttrTransactionMethod`). Inner-node reporting is the existing
ledger and needs no change; §4.2 says why.

**FR-6 — Import maps both attributes verbatim onto the model.**
`transactionOptions` becomes: `method` → `ParseTransactionMethod` →
`WithTransactionMethod`; `protocol`, when present → `WithTransactionProtocol`.
The `transactionMethods` table, the two import-time refusals and the `protocol`
`Dropped` report are removed. A `<transaction method="##Store">` therefore
**imports** and is refused by FR-4 at registration — the same moment a script
format is.

**FR-7 — Nothing about the abort changes.** Every SRD-061 transaction test and
the compensation/cancel e2e paths pass unchanged.

### Non-functional

**NFR-1 — API validation.** Every new public parameter is checked (blank
method, blank protocol, nil characteristics where one is required), with
self-identifying messages.

**NFR-2 — Coverage.** Diff coverage ≥ `COVER_MIN`; every touched function at
100%.

**NFR-3 — No lock-held host call.** The bind step calls no host code (there is
no coordinator seam yet); `make lock-sweep` unchanged.

**NFR-4 — Export stays out.** No exporter change; the SRD records the
deferral (ADR-024 §7 slice 3) rather than a partial write.

## §3 Models

### §3.1 `pkg/model/activities/transaction.go` (new)

The FR-1 shapes, plus the option type:

```go
type TransactionOption func(*TransactionCharacteristics) error
func WithTransactionMethod(m TransactionMethod) TransactionOption
func WithTransactionProtocol(p string) TransactionOption
```

`WithTransaction(opts ...TransactionOption)` builds a
`TransactionCharacteristics{method: TransactionCompensate}` and applies the
options; `subProcessConfig.isTransaction bool` becomes `tx *TransactionCharacteristics`.

### §3.2 `SubProcess`

```go
type SubProcess struct {
    …
    // tx holds the transaction characteristics (ADR-028 §2.1): nil on a
    // plain or Event Sub-Process. Immutable configuration, shared by clones.
    tx *TransactionCharacteristics
}
func (sp *SubProcess) Transaction() *TransactionCharacteristics
func (sp *SubProcess) IsTransaction() bool { return sp.tx != nil }
```

### §3.3 `pkg/thresher/transaction_validation.go` (new)

`validateTransactionCoverage(s *snapshot.Snapshot) error` — the
`validateScriptCoverage` walk over `*activities.SubProcess` nodes with
`Transaction() != nil && Transaction().Method() != TransactionCompensate`.

### §3.4 Runtime binding (`internal/instance`)

`scopeEntry` (`scope_runtime.go:76`) gains `tx *activities.TransactionCharacteristics`,
derived from the entry's `node` at every site an entry is created — the
executor open (`scope_decorator.go:423`) and the two restore paths
(`scope_runtime.go:377`, `:482`), so a rehydrated Transaction scope is bound
exactly as a fresh one; `cancelTransaction` switches on `entry.tx.Method()`.
A single `bindTransaction(node) *TransactionCharacteristics` helper keeps the
three sites one line each.

### §3.5 Worked example — the issue's two files, end to end

```xml
<bpmn:transaction id="tx" name="Charge" method="##Compensate" protocol="wsat">
```

imports (today: refused, "not one of BPMN's…"); `sp.Transaction().Method()`
is `TransactionCompensate`, `Protocol()` is `"wsat"`; `ImportDocument`'s
`Dropped` is empty (today: one `protocol` entry); `RegisterProcess` accepts;
a Cancel inside aborts exactly as before, and the thrown-compensation fact
carries `method=compensate`.

```xml
<bpmn:transaction id="tx" name="Charge" method="##Store">
```

imports (today: refused at import as standing); `Method()` is `"##Store"`;
`RegisterProcess` returns *no transaction coordinator is registered for 1
transaction(s): "Charge" (method "##Store") — this engine coordinates
compensate only (ADR-028 §2.7); model the undo as compensation handlers*.

## §4 Analysis

### §4.1 Why the refusal moves from import to registration

A converter refusing `store` was a model rule living in the converter
(ADR-024 §2.16's forbidden second copy), and it was wrong in the way second
copies get wrong: it knew one spelling. With the value open (ADR-028 §2.7) the
only question is *does this engine have a coordinator for it*, which is a
property of the thresher, not the document — exactly the script-format case,
and it gets the same check at the same moment. The reader still learns before
anything runs.

### §4.2 Why inner-node reporting needs no code

ADR-028 v.2 §2.1's "every inner node reports start/completion/failure to the
coordinator" is, for compensate, the completion ledger: `recordLeafCompletion`
and `recordScopeCompletion` append per completed node, per scope, and
`cancelTransaction` sweeps it. Generalizing the report to a foreign
coordinator is the seam's contract (ADR-028 §2.8), deferred; this landing
binds the scope so the sweep is *dispatched through* the binding, which is
the only structural change the seam will need at this point.

### §4.3 Why `WithTransactionProtocol` is nested, not a `SubProcessOption`

A `SubProcessOption` `WithTransactionProtocol` would be legal to pass without
`WithTransaction`, forcing a post-hoc "protocol on a non-transaction" check
in `NewSubProcess`. Nesting it makes the invalid combination unexpressible.

### §4.4 Why `ParseTransactionMethod` carries unknown values

The schema's `tTransactionMethod` is a union with `anyURI`; the model cannot
know the host's registered coordinators, and rejecting at parse would
re-create the closed table in a new place. The model carries; registration
judges (FR-4).

## §5 API

Added: `TransactionMethod`, `TransactionCompensate`, `ParseTransactionMethod`,
`TransactionCharacteristics` (+ `Method`, `Protocol`), `TransactionOption`,
`WithTransactionMethod`, `WithTransactionProtocol`, `SubProcess.Transaction`,
`observability.AttrTransactionMethod`. Changed: `WithTransaction()` →
`WithTransaction(opts ...TransactionOption)` (source-compatible). Removed:
nothing exported. Converter: `<transaction>` `method`/`protocol` mapped;
two refusals and one `Dropped` entry retired.

## §6 Tests

| # | Test | Asserts | FR |
|---|---|---|---|
| T-1 | `TestParseTransactionMethod` | `""`, `compensate`, `##Compensate` → compensate; `##Store`, `store`, a URI carried as is; surrounding blanks trimmed | FR-1 |
| T-2 | `TestTransactionOptions` | default is compensate with no protocol; method and protocol carried; blank method refused; blank protocol refused; each error names the option | FR-1, FR-2, NFR-1 |
| T-3 | `TestTransactionMarkerAndShapeRules` (extended) | `Transaction()` nil on plain/event, non-nil with `WithTransaction()`; clone shares the characteristics; existing sub-tests unchanged | FR-3, FR-7 |
| T-4 | `TestValidateTransactionCoverage` | compensate registers; `##Store` refused naming the transaction and the method; two offenders sorted; a nested plain sub-process is walked | FR-4 |
| T-5 | `TestTransactionBindingDrivesAbort` | a Cancel inside a compensate transaction aborts as before, and the thrown fact carries `method=compensate`; a scope whose binding is not compensate reports an invariant violation | FR-5, FR-7 |
| T-6 | `TestTransactionMethodDispositions` (rewritten) | absent, `compensate`, `##Compensate` import as compensate; `store`, `image`, `##Store`, `rollback` **import** carrying the value; none reported | FR-6 |
| T-7 | `TestTransactionProtocolIsCarried` (replaces `…IsReported`) | `protocol="wsat"` lands on `Transaction().Protocol()`, `Dropped` empty | FR-6 |
| T-8 | `TestRefusalsSayWhichKindTheyAre` (row retired) | the `transaction method=store` row is removed — it is no longer an import refusal; `TestStoreIsRefusedAtRegistration` pins the wording at its new site | FR-4, FR-6 |
| T-9 | SRD-061 transaction tests, e2e cancel/compensation, `examples/transaction-sub-process` in the run sweep | unchanged and green | FR-7 |

## §7 Milestones

| M | Scope | Commit |
|---|---|---|
| M1 | Model: `transaction.go`, options, `SubProcess.tx`/`Transaction()`, clone; T-1…T-3 | one |
| M2 | Thresher: `validateTransactionCoverage` in `RegisterProcess`; T-4 | one |
| M3 | Runtime: scope binding, `cancelTransaction` dispatch, `AttrTransactionMethod`; T-5 | one |
| M4 | Importer: `transactionOptions` rewrite, table and refusals retired; T-6…T-8 | one |

Doc sync (ADR-024 §2.16's transaction example, the import-coverage guide's
standing row, `converters.md`, CHANGELOG, README sweep) follows as its own
`docs:` commit at the handover step.

## §8 Cross-doc references

| Direction | Document | Why |
|---|---|---|
| up | [ADR-028 v.2](../design/ADR-028-transaction-sub-process.md) §2.1, §2.7, §2.8 | the decision this implements |
| up | [ADR-024](../design/ADR-024-process-interchange-converters.md) §2.16, §7 | the no-second-copy rule; export slice 3 |
| up | [ADR-025](../design/ADR-025-activity-iteration-loop-and-multi-instance.md) §2.1 | the characteristics shape |
| up | [ADR-026](../design/ADR-026-compensation-events.md) §2.2, §2.4 | the ledger and sweep the built-in coordinator is |
| side | [SRD-061](SRD-061-transaction-sub-process.md) | the v.1 landing; frozen |
| side | [SRD-089.E](SRD-089.E-bpmn-import-containers-and-lanes.md) §4.5 | the import dispositions superseded; frozen |

No downward references.

## §9 Definition of Done

1. FR-1…FR-7 implemented and wired; NFR-1…NFR-4 held.
2. §6 tests present and green; `make ci` green (mock-check, lint, race, diff
   coverage ≥ `COVER_MIN`, every touched function 100%).
3. Examples run sweep green.
4. `/check-srd` PASS; §10 filled; ADR-028 v.2 flipped to Accepted and its
   Russian twin refreshed at handover; linked docs synced.
5. PR description carries *Part of #324* with the export deferral stated.

## §10 Implementation summary

_Filled at landing._

## Open questions

None.
