# SRD-097 — Association expressions: transformation, assignment, and multi-source

| Field | Value |
|---|---|
| Status | Accepted |
| Date | 2026-08-28 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-011 v.10](../design/ADR-011-process-data-flow.md) §2.4 (the three shapes and what their expressions address), §2.9.2/§2.9.3 (path read and write), §2.3 (availability gates, never waits) |
| Upstream | [ADR-002](../design/ADR-002-extension-architecture.md) (the `ExpressionEngine` seam the shapes evaluate through); [ADR-024](../design/ADR-024-process-interchange-converters.md) §2.16 (a converter keeps no second copy of a model rule), §2.14 (the vendor dialect this does **not** map) |
| Related | [SRD-063](SRD-063-dataobject-scope-integration.md) §10.3 (the deferral this pays), [SRD-068](SRD-068-datastore-port.md) (the Data Store half of the copy path), [SRD-094](SRD-094-event-data-attachment.md) (events reach the same copy path) |
| Closes | [#328](https://github.com/dr-dobermann/gobpm/issues/328) |

## §1 Background

ADR-011 has prescribed the standard's three association shapes since v.1 and,
in v.10, what their expressions address. One shape is live.

**The live copy path is `pkg/model/dataflow`.** `FillInput` (`dataflow.go:29`)
and `PushOutput` (`:50`) are what every task runs — `task.LoadData`
(`pkg/model/activities/task.go:116`) and `task.UploadData` (`:221`) call them,
and since SRD-094 events do too. Each takes the association's *first* source
name, resolves that datum from the frame or a Data Store, copies the value and
flips the target Ready: shape 3 and nothing else. The comment above
`pushToScope` says so — *"(A transformation/assignment is a noted follow-up —
SRD-063 §10.3.)"*

**A second, mostly-dead path already evaluates a transformation.**
`Association.calculate` (`pkg/model/data/association.go:277`) implements shapes
1 and 3: with `a.transformation != nil` it calls
`a.transformation.Evaluate(ctx, a)` — the association is its own `data.Source`
via `Find` (`:322`) — else it copies the single source. It writes into the
association's **own** `a.target` `ItemAwareElement`, not into the frame
instance or the scope datum the live path writes, and it is reached only from
`Association.Value` (`:228`) and `UpdateSource` (`:154`), whose only caller in
the tree is `pkg/model/data_objects/data_object.go:295,320`.

**Assignment has no model.** The whole `Assignment` interface is commented out
(`association.go:343-366`).

**The model's window on engine seams has no expression engine.**
`exec.Frame` (`pkg/exec/frame.go:31`) exposes `GetData`, `GetDataByID`,
`DataStores() datastore.Registry` (`:60`), `RecordDataMovement`, `SetReceived`
— nothing that evaluates. `internal/scope.Frame` (`internal/scope/frame.go:27`)
is the implementation; `NewFrame` (`:101`) has one caller,
`internal/instance/scope.go:122`. The engine itself is reachable at
`renv.EngineRuntime.ExpressionEngine()` (`pkg/renv/engineruntime.go:33`,
implemented by `internal/enginert.Runtime` at `enginert.go:199`) — the runtime
layer evaluates conditions that way (`internal/instance/conditional.go:369`,
`mi.go:145`, `adhoc.go:112`), but the model layer cannot reach it.

**The path machinery the shapes need already exists.** `data.ResolvePath`
(`pkg/model/data/path.go:189`) walks a read; `values.SetPath`
(`pkg/model/data/values/setpath.go`) walks a write, vivifying intermediates and
setting the last step through `Record.SetField` / `Collection.SetAt`, and
errors on an empty path — *"a whole-value write is `Value.Update`"*. Its only
caller today is the worker output mapping (`pkg/tasks/outputmapping.go:149`),
so §2.9.3's association half has never been wired. `Frame.GetData` already
resolves structural paths (`internal/scope/frame.go:289`) and `SOURCE/addr`
provider addresses (`:281`).

**The importer parses the two elements as flags and skips their bodies.**
`dataAssocSpec` (`pkg/convert/bpmn/dataflow.go:54`) carries `hasTransformation`,
`hasAssignment` and `extraSources`; the parse sets the flag and calls
`p.skipElement()` (`:717-725`). `dataassoc.go:74-92` then refuses all three
cases, naming #328. Expression minting exists and is reusable: `exprSpec` +
`runnableBody` resolve the language and translate JUEL (`language.go:123`),
with `newBoolExpression`/`newIntExpression` (`:144`, `:156`) as the two
result-typed variants.

## §2 Requirements

### Functional

**FR-1 — `data.Assignment` becomes a real model type.**

```go
// Assignment is one from→to mapping of a data association (BPMN §10.4.2
// rule 2, ADR-011 §2.4): from is evaluated for a value, to is the data
// path that value is written to.
type Assignment struct {
    from FormalExpression
    to   string
    foundation.BaseElement
}

func NewAssignment(from FormalExpression, to string, baseOpts ...options.Option) (*Assignment, error)
func (a *Assignment) From() FormalExpression
func (a *Assignment) To() string
```

`from` must be non-nil, `to` non-blank and parseable by `data.SplitPath`.

**The `to` path is absolute, and its head names the association's target** —
the scope datum on an output association, the activity's parameter on an
input one — with the remaining steps addressing inside it. A head-only `to`
("order") is the whole-value write; "order.status" writes one field.
`Assignment.ToHead()` performs that split once, so the validator, the copy
path and the converter all read one answer, and `values.SetPath` receives
exactly the relative remainder it expects. A head naming anything but the
target is a modelling error, refused where it is read. The commented-out
interface is deleted. `WithAssignments(as ...*Assignment) options.Option`
attaches them to an association; an association may carry a transformation
**or** assignments, never both — ADR-011 §2.4's engine choice, not a rule of
the standard — and that is refused at construction.

**FR-2 — the frame carries the expression engine.** `exec.Frame` gains

```go
// ExpressionEngine returns the engine an association's transformation or
// assignment evaluates through (ADR-011 §2.4, ADR-002). Nil for a
// transient evaluation frame with no engine wired — an association that
// needs one then fails fast rather than dereferencing it.
ExpressionEngine() expression.Engine
```

mirroring `DataStores()` in shape, nil-ability and failure posture.
`internal/scope.Frame` holds it; `NewFrame` takes it from the runtime the
instance already has.

**FR-3 — the copy path dispatches on the shape, in both directions.**
`FillInput` and `PushOutput` choose per association:

| Shape | What runs |
|---|---|
| `transformation != nil` | evaluate it once; the result **replaces** the target's value |
| `len(assignments) > 0` | evaluate each `from` in declaration order; write each result at its `to` path |
| neither | today's single-source copy, byte-identical |

The scope half and the Data Store half both dispatch: a transformation whose
target is a `DataStoreReference` writes the store exactly as a plain copy does.
Each movement is still recorded through `RecordDataMovement`.

**FR-4 — expressions evaluate against the activity's data context.** The
`data.Source` handed to `Engine.Evaluate` resolves names through the frame
(`f.GetData`), so an expression reads the association's sources, the node's
other data, and structural paths into any of them — the ADR-011 §2.9.2
resolver, not a second one. The write side: a `to` with steps goes through
`values.SetPath` on the target's value; a step-less `to` (and every
transformation) is a whole-value `Value.Update`.

**FR-5 — sources gate by shape, and the model says so.** With neither
expression shape, exactly one source is legal (§10.4.2 rule 3): a second
`WithSource` on a transformation-less, assignment-less association is refused
at construction. `asscConfig.Validate` (`pkg/model/data/data_options.go:184`)
already enforces this for a transformation; the rule extends to assignments,
which combine several sources through their own expressions just as a
transformation does. Availability is unchanged — an unavailable source fails fast
(§2.3), never waits, for every shape.

**FR-6 — the second evaluator is bounded, not merged.** `Association.calculate`
belongs to the legacy `flow.DataNode` path (`DataObject.Update`), which
**has no runtime caller** — only its own two package tests. Routing it
through FR-3's dispatch is impossible as first written: the dispatch reads
the engine off the execution frame (FR-2) and that path has no frame. So
the second evaluator is bounded instead: `calculate` keeps the
transformation branch it can honestly evaluate (a `FormalExpression` over
the association's own sources) and **refuses an assignment-bearing
association**, because plain-copying one would silently discard a declared
mapping and evaluating a different shape than the document declared is how
two evaluators drift (ADR-024 §2.16, one layer down). `calculate`,
M9 then deleted the path outright rather than leaving a bounded corpse
behind: code with no value is not kept. `flow.DataNode`,
`DataObject.Update`, `Association.UpdateSource`/`Value`/`calculate` and the
`Recalculate`/`NoRecalculate` constants are gone — 527 lines removed against
27 added. It is a removal from an exported surface, which at v0.11.0 carries
no compatibility promise, and nothing in the repository, the guides or the
examples used any of it. `Association.IsReady` survives: it is a plain state
query, the only read of the target's state on the type, and live tests use
it.

**FR-7 — the importer maps all three constructs.** `<transformation>` and
each `<assignment>`'s `<from>`/`<to>` are parsed for real (body + language,
through the existing `exprSpec`/`runnableBody` machinery, JUEL translated as
elsewhere) and mapped onto FR-1's options; a second `<sourceRef>` is mapped
when a transformation is present. The three refusals at `dataassoc.go:74-92`
are removed.

**FR-8 — a `to` that is not a path is refused where it is read.** The
importer resolves `<to>`'s body to a data path: a bare path, or a JUEL-style
`${…}` wrapper around one. Anything else — a call, an arithmetic expression,
anything the model cannot address — is refused by the importer naming the
association and the body, never imported as a silent whole-value copy.

### Non-functional

**NFR-1 — public-API validation.** Every new exported constructor and option
checks its parameters with self-identifying errors.

**NFR-2 — no change for a plain association.** An association with neither
shape behaves byte-identically; the SRD-063/SRD-068/SRD-094 suites pass
untouched.

**NFR-3 — coverage.** Diff coverage ≥ `COVER_MIN`, every touched function at
100%.

**NFR-4 — no host call under a lock.** `make lock-sweep` stays clean: the
engine's `Evaluate` is host-supplied code, and the copy path must not hold a
lock across it.

## §3 Models

### §3.1 `pkg/model/data` — the assignment and the association's shape

FR-1's `Assignment`, `WithAssignments`, and the construction rules
(transformation XOR assignments; single source without either). `Association`
gains `Assignments() []*Assignment` and keeps `Transformation()`.

### §3.2 `pkg/exec` + `internal/scope` — the engine seam

FR-2's accessor on the interface, the field on `scope.Frame`, the parameter on
`NewFrame`, and the one call site in `internal/instance/scope.go`.

### §3.3 `pkg/model/dataflow` — the dispatch

```go
// evaluated returns the value an association produces for its target, and
// whether the association produced one at all.
func evaluated(ctx context.Context, f exec.Frame, a *data.Association, src data.Data) (data.Value, bool, error)

// applyAssignments writes each assignment's result at its path.
func applyAssignments(ctx context.Context, f exec.Frame, a *data.Association, target data.Value) error

// frameSource adapts the execution frame to data.Source.
type frameSource struct{ f exec.Frame }
func (s frameSource) Find(ctx context.Context, name string) (data.Data, error)
```

### §3.4 `pkg/convert/bpmn` — the parse

`dataAssocSpec` replaces its two booleans with the parsed shapes:
`transformation *exprSpec`, `assignments []assignSpec{from *exprSpec; to string}`;
`extraSources` stays and is mapped rather than refused when a transformation is
present.

### §3.5 Worked example — a transformation, end to end

```xml
<bpmn:dataInputAssociation id="a1">
  <bpmn:sourceRef>order</bpmn:sourceRef>
  <bpmn:sourceRef>customer</bpmn:sourceRef>
  <bpmn:transformation>order.total * customer.discount</bpmn:transformation>
  <bpmn:targetRef>chargeAmount</bpmn:targetRef>
</bpmn:dataInputAssociation>
```

imports (today: refused twice over — transformation and multi-source); the
association carries the expression and both sources; at
`task.LoadData` the engine evaluates it against the frame — `order.total` and
`customer.discount` resolve by path through `Frame.GetData` — and the result
replaces `chargeAmount`'s value, which flips Ready and gates the task's start
exactly as a copied one does.

```xml
<bpmn:dataOutputAssociation id="a2">
  <bpmn:sourceRef>result</bpmn:sourceRef>
  <bpmn:assignment>
    <bpmn:from>result.code</bpmn:from>
    <bpmn:to>order.status</bpmn:to>
  </bpmn:assignment>
  <bpmn:targetRef>order</bpmn:targetRef>
</bpmn:dataOutputAssociation>
```

imports; at `task.UploadData` `result.code` is evaluated and written into the
per-instance `order` Data Object's `status` field through `values.SetPath` —
the owner enforcing its own shape, so a typed `order` rejects an unknown
`status` field by construction.

The third case is the refusal (FR-8):

```xml
<bpmn:assignment>
  <bpmn:from>result.code</bpmn:from>
  <bpmn:to>concat(order.status, "-done")</bpmn:to>
</bpmn:assignment>
```

does not import: `<to>` is not a path, and the import fails naming the
association and the body — *"assignment `to` `concat(order.status, "-done")`
is not a data path; an assignment writes at a path (ADR-011 §2.4) — compute
the value in `from` instead"*. It is never imported as a whole-value copy,
which would silently discard the modeller's intent.

## §4 Analysis

### §4.1 Why the engine rides the frame

The copy path is model-layer code that runs inside the model's own
`LoadData`/`UploadData`, and the frame is the model's one window on
engine-provided seams — `DataStores()` is exactly this shape already:
engine-global, shared across instances, nil for a transient evaluation frame,
with a fail-fast on use. Threading an engine through `task.LoadData`'s
signature instead would touch every node kind that has data; moving the
dispatch into `internal/instance` would split one association's execution
across two layers. The frame is where the seam belongs.

### §4.2 Why the expression sees the frame, not just the sources

`Association.Find` resolves the association's own `sources` map — model-side
elements whose values the live path never populates, because it reads data
from the frame by name. Handing the expression a frame-backed `data.Source`
gives it the same resolution every other consumer gets (§2.9.2's "every
consumer navigates through the one resolver"), including structural paths and
`SOURCE/addr` provider reads. The standard constrains what **gates** an
association (its sources' availability), not what its expression may read.

### §4.3 Why `to` is a path in the model but a body in the document

`values.SetPath` takes a path string and writes through the owner's
capability; that is the write contract ADR-011 §2.9.3 fixed. A document's
`<to>` is an expression *element*, so the importer resolves its body to that
path (FR-8) — the one place where a document's freedom meets the model's
narrowing, and the place a refusal is honest. Keeping `to` a `FormalExpression`
in the model would carry a body nothing evaluates.

### §4.3a Why the head is the target rather than a free name

An assignment could in principle write anywhere the frame can address, which
is what §10.4.2's "any element in context" literally permits. Binding the head
to the association's own target keeps one association responsible for one
target: the availability gate, the `Dropped` report and the observability
movement all name that target, and an assignment that wrote elsewhere would
make each of them lie. A mapping that needs to write two data elements is two
associations, which is also how a modeller draws it.

### §4.4 Why the multi-source rule is validated in the model

The converter must keep no second copy of a model rule (ADR-024 §2.16), and
"more than one source needs a transformation" is a property of the association,
not of XML. Validated at construction, it holds for a programmatically built
process too, and the importer simply propagates the error.

### §4.5 What happens to `calculate`

Retiring it outright would strand `data_objects`; leaving it would leave two
evaluators. FR-6 routes both through one: the dispatch takes the value it
needs and `calculate` becomes its caller rather than a rival implementation.

## §5 API

Added: `data.Assignment`, `NewAssignment`, `Assignment.From`/`To`,
`data.WithAssignments`, `Association.Assignments`, `Association.Transformation`,
`exec.Frame.ExpressionEngine`. Changed: `scope.NewFrame` takes the engine
(internal). Removed: the commented-out `Assignment` interface. Converter:
`<transformation>`, `<assignment>` and multi-source map; three refusals go.

## §6 Tests

| # | Test | Asserts | FR |
|---|---|---|---|
| T-1 | `TestNewAssignment` | from/to carried; nil `from` refused; blank `to` refused; an unparseable `to` refused; each error names the constructor | FR-1, NFR-1 |
| T-2 | `TestAssociationShapeRules` | transformation and assignments together refused; a second source without a transformation refused; both legal with one | FR-1, FR-5 |
| T-3 | `TestFrameExposesExpressionEngine` | the frame returns the runtime's engine; a frame built without one returns nil | FR-2 |
| T-4 | `TestFillInputTransformation` | an input association's transformation is evaluated against the frame and replaces the target; sources resolve by path | FR-3, FR-4 |
| T-5 | `TestPushOutputAssignments` | each assignment writes at its path into the scope datum; declaration order respected; a step-less `to` replaces the whole value | FR-3, FR-4 |
| T-6 | `TestExpressionShapeOverDataStore` | a transformation whose target is a `DataStoreReference` writes the store | FR-3 |
| T-7 | `TestExpressionAssociationFailsFast` | an unavailable source fails fast, never waits; a nil engine on an expression-bearing association fails with a self-identifying error | FR-4, FR-5, NFR-2 |
| T-8 | `TestPlainAssociationUnchanged` | a shape-less association's movement and facts are byte-identical | NFR-2 |
| T-9 | `TestTransformationImports` / `TestAssignmentImports` | §3.5's two documents import; the model carries the expressions; `Dropped` empty | FR-7 |
| T-10 | `TestMultiSourceImportsWithTransformation` | two `<sourceRef>`s import with a transformation and are refused without one | FR-5, FR-7 |
| T-11 | `TestToMustBeAPath` | a `<to>` that is not a path is refused naming the association and the body | FR-8 |
| T-12 | the SRD-063/068/094 suites, the examples run sweep | unchanged and green | NFR-2 |

## §7 Milestones

| M | Scope | Commit |
|---|---|---|
| M1 | `data.Assignment`, `WithAssignments`, the shape rules; T-1, T-2 | one |
| M2 | The frame's engine seam (`exec.Frame`, `scope.Frame`, `NewFrame`, the call site); T-3 | one |
| M3 | The dataflow dispatch, both directions and both halves, with `frameSource`; T-4…T-8 | one |
| M4 | `calculate` reconciled through the one evaluator; the `data_objects` path proven unchanged | one |
| M5 | The importer: real parsing, mapping, the three refusals retired; T-9…T-11 | one |
| M6 | An example carrying a transformation and an assignment end to end | one |

Doc sync (ADR-024 §2.16's rows, the import-coverage guide, the data guides,
README, CHANGELOG, conformance-status) follows as its own `docs:` commit at the
handover step.

## §8 Cross-doc references

| Direction | Document | Why |
|---|---|---|
| up | [ADR-011 v.10](../design/ADR-011-process-data-flow.md) §2.4, §2.9.2, §2.9.3, §2.3 | the decision this implements |
| up | [ADR-002](../design/ADR-002-extension-architecture.md) | the expression-engine seam |
| up | [ADR-024](../design/ADR-024-process-interchange-converters.md) §2.16, §2.14 | no second copy of a model rule; the vendor dialect left alone |
| side | [SRD-063](SRD-063-dataobject-scope-integration.md) §10.3 | the deferral this pays |
| side | [SRD-068](SRD-068-datastore-port.md) | the Data Store half of the copy path |
| side | [SRD-094](SRD-094-event-data-attachment.md) | events share the copy path |

No downward references.

## §9 Definition of Done

1. FR-1…FR-8 implemented and wired; NFR-1…NFR-4 held.
2. §6 tests present and green; `make ci` green (mock-check, lint, race, diff
   coverage ≥ `COVER_MIN`, every touched function 100%).
3. Examples run sweep green, including M6's.
4. `/check-srd` PASS; §10 filled; ADR-011 v.10 flipped to Accepted and its
   Russian twin refreshed at handover; linked docs synced.
5. PR description carries `Closes #328`.

## §10 Implementation summary

### §10.1 Milestones as landed (branch `feat/association-expressions`)

| M | Commit | Landed |
|---|---|---|
| doc | `a387c087`, `e068c077` | ADR-011 v.10, and the correction the SRD review found in it |
| doc | `c344dbfd` | this document |
| M1 | `314cdc69` | `data.Assignment`, `WithAssignments`, the shape and cardinality rules; T-1, T-2 |
| M2 | `44ff0df3` | `exec.Frame.ExpressionEngine()`, wired through `instanceScope`; T-3 |
| M3 | `0dc125ea` | the dispatch: `frameSource`, `evaluate`, `applyShape`, all four copy paths; T-4…T-8 |
| M3a | `a7c870c0` | a pre-existing flake in the transaction restore captures (see §10.2) |
| — | `dacf74b8` | the defensive `opErr` returns on one line, the form the coverage gate reads |
| M4 | `4cc6215d` | the legacy evaluator bounded; T-6's refusal |
| M5 | `33f1e713` | the importer maps all three shapes; the three refusals retired; T-9…T-11 |
| M6 | `44f645b8` | `examples/association-expressions`, both shapes end to end |
| M6a | `99119659` | the diff-coverage gaps closed |
| — | `a70c7cc5` | master merged (PR #358, SRD-090.D) |
| — | `b5365b15` | this summary |
| M7 | `97ca4310` | the independent review's six findings (see §10.2) |
| — | `1b21f016` | the downstream docs synced with the shapes |
| M8 | `b3675717` | the gate's heartbeat, which was dead on macOS (see §10.2) |
| — | `f08e987c` | §10.3 points at the run that verified the tip |
| M9 | `33d0e697` | the dead `flow.DataNode` evaluator deleted (see §10.2) |
| M10 | `PENDING` | two checkpoint-capture flakes of issue #356 (see §10.2) |

### §10.2 Where reality diverged from the draft

- **FR-2 is a setter, not a constructor parameter.** §3.2 said `NewFrame`
  would take the engine. It does not take the Data Store registry either —
  `instanceScope.openFrameAt` wires that with `SetDataStores` after
  construction — and inventing a second wiring style for the same kind of
  engine-global seam is worse than following the one that exists.

- **FR-6 deletes the second evaluator rather than merging or bounding it.**
  The SRD asked for one evaluator. M4 bounded the legacy one — it kept the
  transformation branch it could honestly evaluate and refused an
  assignment-bearing association — and deferred the removal as "a
  public-interface change and its own decision". That deferral did not
  survive review: code with no caller has no value, and keeping it means the
  next reader must work out which of two evaluators is real. **M9 removes
  it**: `flow.DataNode`, `DataObject.Update`,
  `Association.UpdateSource`/`updateSrc`/`Value`/`calculate` and the
  `Recalculate`/`NoRecalculate` constants, 527 lines out against 27 in.
  Nothing outside the deleted code used any of it — no guide, no example, no
  runtime path — and at v0.11.0 an exported-surface removal carries no
  compatibility promise. `Association.IsReady` stays: it is a state query
  independent of the evaluator, the only read of the target's state on the
  type, and `pkg/model/events` asserts through it.

  Three test suites had been asserting the deleted path's **side effects**
  without saying so. `activities.TestTaskData` asserted a task input held
  `100` after an association was merely bound — it held that only because
  `ia.Value(ctx)`, called two lines earlier, ran `calculate` and wrote the
  target. Binding moves no data, so the assertion now reads `42`, the
  declared value, with the reason written down. `data_objects.TestUpdate`
  tested the deleted method and is gone; the transformation mocks that
  survive in that package are marked `Maybe()`, because a model-only test
  carries an expression and never evaluates it.

- **FR-7's multi-source needed two things the draft did not name.** The
  parse collapsed several parameter-side refs into one (`setEnd` overwrote
  `paramRef`), so an OUTPUT association silently lost its extra sources —
  `extraParams` now keeps them, and they resolve to item ids because that is
  what `Associate*` matches on. And an INPUT association's extra sources
  reach the model as `data.WithSource` options through M5's `shape` tail, for
  which `DataObject` and `DataStoreReference` gained `ItemAware()`. Neither
  needed a new attach API. A first attempt fed `extraSources` to
  `AssociateSource` — wrong on both counts, since on an output association
  those refs are extra *targets*, which §10.4.1 forbids and the converter now
  refuses.

- **The item-match rule was too broad.** §10.4.1's "the two ends'
  itemDefinitions must match" holds for a plain copy only: a transformation
  replaces the target and an assignment writes what its own expression
  produced, so neither is a copy between two items. The converter's check now
  skips a shaped association.

- **M6 forced two engine changes no unit test could have demanded.** An
  expression now resolves the **node's own parameters** as well as the scope:
  an output association's `from` exists to shape what the node just produced,
  and at that moment the output is a frame instance, which the scope does not
  hold by name — so `from` could read anything except the value it was
  written for. And an unavailable source now says **which** source and
  **why**: the check returned a bool and the caller reported "not Ready" for
  both a name the data context does not hold (a modeling error) and a datum
  not produced yet. They need different fixes, and the conflation cost a
  debugging round.

- **Two pre-existing test flakes were fixed** (the no-pre-existing-errors
  rule). `atHold`, the capture predicate the transaction restore tests share
  (mine, from SRD-095), matched a shape that is also true *before* the
  activity ledgers, so under load the capture returned a document with no
  ledger entry; it now requires a track parked at the wait.
  `TestCompositeHostReportsHostingScope` found the host track and then
  asserted `TrackHostingScope` immediately, which is a window the flip has
  not always entered; the state is now part of the predicate.

- **The merge brought reserved data names** (`pkg/model/data/reserved.go`,
  SRD-090.D): names the engine owns and a model may not declare. They compose
  with this landing without a change — an assignment's `to` head must equal
  its association's target name, and a target cannot *be* a reserved name, so
  those paths are refused transitively; a `from` expression, meanwhile, can
  now read `loopCounter` and its siblings through the frame.

- **M7: the review found one defect the tests could not.** Strengthening two
  importer assertions showed that both `<assignment>`s of an association
  minted the same expression id (`<assoc>:assignment.from`), so the second
  shadowed the first wherever expressions are held by id. Each assignment now
  owns its expression — by its own id, or by its index within the association
  when the file gives it none. Fixing that exposed a second wording defect of
  this branch's own making: `assocExpr` hard-coded `dataInputAssociation` as
  the owner kind, so an unrunnable expression on an *output* association, or
  in an assignment, was refused under the wrong element's name. The refusal
  now names what carries the expression, which is what a modeller must edit.

- **M7: two engine readings the review argued for, and one it was wrong
  about.** An assignment whose target is a Data Store now reads the record
  before writing it back (`storeTarget`) — the store's `Put` replaces a key
  outright, so the data-object path's build-then-put would have dropped every
  field the assignment did not name; a transformation still replaces the key,
  which is what a transformation means. A source the association takes from a
  store now resolves inside an expression. A node's own output parameter now
  wins over a same-named scope datum. Movement facts name the sources they
  read, one per source. Two notes the review filed as blockers were verified
  **wrong** — both misread the same `AssociateSource` loop, from opposite
  directions — and are rejected with their reasons in the review triage; no
  code changed for them.

- **M8: the gate's own heartbeat was dead on this platform.** Running the
  final gate showed `setsid: command not found` once per step and not one
  elapsed line. `setsid` is util-linux — Linux has it, macOS does not, and
  Homebrew's is keg-only — and it is what puts the heartbeat in its own
  process group. So the feature FIX-039 built to make a long silence readable
  had never worked on the platform this engine is developed on, and its
  absence was reported as noise rather than as a missing tool. The launcher
  is now a prefix that empties when the tool is absent, and the heartbeat is
  stopped by killing its `sleep` by parent instead of by group. Verified both
  ways: the heartbeat prints, an interrupted run still records
  `interrupted by a signal`, and neither path leaves a stray `sleep` or an
  orphaned `golangci-lint`. Found here, so fixed here.

- **M10: two of issue #356's gate flakes, diagnosed and fixed.** The gate
  went red at `test-core` after M9 on `TestCompositeRestoreRunsBodyOnce` and
  `TestARestoredFanOutKeepsTheEligibilityItAnnounced` — the first named by
  [#356](https://github.com/dr-dobermann/gobpm/issues/356), the second not.
  Neither is caused by M9, and neither reproduces alone: 15 isolated runs,
  4 whole-package runs under the gate's CPU budget and 4 runs of the gate's
  own `test-core` step all passed.

  They share one defect, and it is in the tests: **a capture predicate that
  does not require the evidence its test then asserts**. Both poll the
  repository for the newest checkpoint and take the first that matches, so a
  predicate satisfied by an *earlier* checkpoint silently supplies the wrong
  document — which load makes likelier by shifting the poll relative to the
  engine's transitions. `TestCompositeRestoreRunsBodyOnce` matched on
  `openScope && len(Tracks) == 2`, which is equally true at the
  host-opens-scope checkpoint, where the body's pass is not recorded; a
  restore from that one re-runs the body *correctly*, and the test read it as
  the FR-5 double-execution regression. The fan-out test matched on three
  recorded `TaskID`s but then asserted each instance's `Eligible` verdict,
  which the announcement records second. Both predicates now require what
  their test asserts — the parked track, and the verdict.

  The two remaining #356 tests (`TestWaitCheckpointCarriesPredecessorLedger`,
  `TestForeignBindingSurvivesRestore`) are **left alone deliberately**. They
  did not fail here and did not reproduce, and their premise is sound: the
  SRD-094 FR-8 fix (`0ca7347f`) emits `evMoved` *before* the wait is declared
  precisely so the wait checkpoint carries the predecessor's ledger. Making
  their shared `atHold` predicate require a ledger entry would make them pass
  — by walking past exactly the ledger-less wait checkpoint whose absence
  they exist to prove. That is a mask, not a fix, so it wants a reproduction
  first. #356 keeps them.

- **A model constraint worth knowing.** An association keys its sources by
  `ItemDefinition` id, so two sources of the *same* item type collide with
  the model's own "duplicate source" error. Distinct item definitions per
  source is the common case and the one the tests and the example use.

### §10.3 Verification

`make ci` at `81a55cb7` (the last doc commit): **PASS — 14/14 steps** in
5m50s (`.ci/last-run.json`), race tests green, diff-coverage **97.1% of 491
changed coverable lines** (min 95%), govulncheck clean, all examples executed
end to end by the run sweep, `make lock-sweep` clean, `make link-check`
clean. That run is also M8's own verification: it printed six heartbeat lines
and no `setsid` diagnostic, where the run before the fix printed fourteen
diagnostics and no heartbeat.

Two earlier full runs bracket the work: `a70c7cc5` (the merge of master) was
PASS 14/14 in 8m54s at 98.4% of 437 changed lines, and `1b21f016` (before the
gate fix) PASS 14/14 at the same 97.1% of 491 — M7 added 54 changed lines,
and the two rejected review findings added none.

Every §6 test row exists and passes; the twelve are spread across
`pkg/model/data`, `internal/scope`, `pkg/model/dataflow` and
`pkg/convert/bpmn`. The FR→test traceability is complete: FR-1/FR-5 → T-1,
T-2; FR-2 → T-3; FR-3/FR-4 → T-4…T-7; FR-6 → the legacy path's refusal test;
FR-7/FR-8 → T-9…T-11 plus the eight importer refusals; NFR-2 → T-8 and the
untouched SRD-063/068/094 suites.

## Open questions

None.
