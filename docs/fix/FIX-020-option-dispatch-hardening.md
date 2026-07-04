# FIX-020 «Every option type carries a dead config-type assertion (with reflection) in its `Apply` method»

**Type:** FIX (one-shot bug-fix; not rewritten after landing).
**Status:** Accepted (2026-07-04, branch `test/harden-core-coverage`, landed `8aacd72`).
**Date:** 2026-07-04.
**Author:** dr-dobermann.
**Branch:** `test/harden-core-coverage` (folded into the in-flight core-hardening work; surfaced during [FIX-019] while making error paths reflection-free).
**Paired doc:** none (local to the `options` mechanism across `pkg/model`).
**Upstream:** self-contained per hierarchy-rule (no ADR/SAD governs the options utility).

**Grounded in (internal artifacts):**
- `pkg/model/options/option.go` (the `Option`/`Configurator` interfaces).
- The 19 option-type files + ~18 dispatching constructors (inventory in §1–§2).
- Reference implementations already hardened: `pkg/model/activities/receive_task.go:71`, `send_task.go:52`.

---

## §1 Symptoms

A design/dead-code defect, the sibling of [FIX-019]. `options.Option` is
`interface { Apply(cfg Configurator) error }`. **21 option types** across 7
packages are func types (`func(*Xconfig) error`) that implement `Apply` by
type-asserting the generic `Configurator` back to their concrete config and,
on failure, returning an error carrying `reflect.TypeOf(cfg).String()`:

```go
// pkg/model/activities/activity_options.go:211
func (ao ActivityOption) Apply(cfg options.Configurator) error {
	if ac, ok := cfg.(*activityConfig); ok {
		return ao(ac)
	}
	return errs.New(errs.M("cfg isn't an activityConfig"),
		errs.C(errorClass, errs.InvalidParameter, errs.TypeCastingError),
		errs.D("cfg_type", reflect.TypeOf(cfg).String()))  // dead + reflective
}
```

Three consequences:

### §1.1 The assertion branch is unreachable dead code

Every constructor that dispatches these options **already** type-switches to
identify the option and passes it the **matching** config (e.g.
`newActivity` calls `o.Apply(&cfg)` with `&cfg` always `*activityConfig`). So
`cfg.(*activityConfig)` always succeeds; the `else` never runs. All 21 rows in
the §2 inventory are dead. Proof: `NewReceiveTask`/`NewSendTask`
(`receive_task.go:71`, `send_task.go:52`) already bypass `Apply` and call the
option func directly — their `Apply` methods are already orphaned.

### §1.2 Runtime reflection in the option layer

The dead branches carry `reflect.TypeOf(cfg).String()` (19 of the 21). FIX-019
removed reflection from `errs`; this is the reflection those error paths still
reference on the option-construction path. (Two — `ComplexOption`,
`EventBasedOption` — have the dead assertion but no reflect.)

### §1.3 Redundant indirection

`Apply(Configurator)` **erases** the concrete config type only to re-assert it
one call later — a round trip that exists solely to satisfy a single interface
method the constructors don't actually need (they've already narrowed the type
in their switch).

**Impact:** 21 dead branches, 19 reflective, and a redundant interface method
whose only real polymorphic consumer (`foundation.NewBaseElement`) can be a
type-switch too.

---

## §2 Root Cause Analysis

### §2.1 The erased-type round trip

`options.Option.Apply(cfg Configurator)` is the sole unifier that lets
`New(name string, ...options.Option)` take a **heterogeneous** option bag. But
`Configurator` erases the concrete config type, so each option must re-assert
it — and that re-assertion is the dead branch. The constructors, however,
already `switch opt.(type)` to route options; once the switch has matched
`case ActivityOption`, the concrete option type is known and its func can be
called with the concrete config directly. The `Apply` indirection is redundant
with the switch the constructors already perform.

### §2.2 Inventory — the dead `Apply`-assertion sites (category **(a)**)

| Option type | Type def | `Apply` | reflect(cfg) | Underlying func |
|---|---|---|---|---|
| `startOption` | events/start_options.go:16 | :56 | :62 | `func(*startConfig) error` |
| `endOption` | events/end_options.go:15 | :36 | :42 | `func(*endConfig) error` |
| `EventOption` | events/event_options.go:44 | :48 | :54 | `func(eventConfig) error` (iface) |
| `ActivityOption` | activities/activity_options.go:30 | :211 | :219 | `func(*activityConfig) error` |
| `taskOption` | activities/task_options.go:19 | :22 | :29 | `func(*multyInstance) error` |
| `RcvTaskOption` | activities/receive_task_options.go:26 | :29 | :39 | `func(*rcvTaskConfig)` (already bypassed) |
| `RoleOption` | activities/role_option.go:20 | :23 | :31 | `func(RoleConfigurator) error` (iface) |
| `SndTaskOption` | activities/send_task_options.go:25 | :28 | :38 | `func(*sndTaskConfig)` (already bypassed) |
| `UsrTaskOption` | activities/user_task_options.go:30 | :232 | :240 | `func(*usrTaskConfig) error` |
| `asscOption` | data/data_options.go:84 | :161 | :166 | `func(*asscConfig) error` |
| `PropertyOption` | data/property_option.go:21 | :72 | :80 | `func(PropertyAdder) error` (iface) |
| `itemOption` | data/item_options.go:18 | :74 | :80 | `func(*itemConfig) error` |
| `IAEOption` | data/item_options.go:104 | :169 | :174 | `func(*iaeConfig) error` |
| `IAEAdderOption` | data/item_options.go:207 | :238 | :246 | `func(IAEAdder) error` (iface) |
| `sflowOption` | flow/sequenceflow_options.go:20 | :23 | :31 | `func(*sflowConfig) error` |
| `NameOption` | options/name_option.go:19 | :22 | :30 | `func(NameConfigurator) error` (iface) |
| `GatewayOption` | gateways/gateway_options.go:20 | :60 | :68 | `func(*gatewayConfig) error` |
| `ComplexOption` | gateways/complex.go:125 | :128 | — | `func(*complexConfig) error` |
| `EventBasedOption` | gateways/event_based.go:63 | :66 | — | `func(*eventBasedConfig) error` |
| `ciOption` | hinteraction/consinp/consinp_options.go:22 | :187 | :195 | `func(*consInpConfig) error` |
| `BaseOption` | foundation/options.go:24 | :33 | :39 | `func(*baseConfig) error` |

The 5 interface-typed options (`EventOption`, `RoleOption`, `PropertyOption`,
`IAEAdderOption`, `NameOption`) still harden cleanly: the config the
constructor passes **implements** that interface, so a direct `o(&cfg)`
compiles — which is exactly why the assertion never fails.

### §2.3 Reachable input-validation guards (category **(b)** — MUST STAY)

Distinct from (a): the constructor-level `default:` "invalid option type" arms
fire on genuine caller misuse (a wrong option passed to a constructor). They
reflect on `o` (the option), not `cfg` — the reliable (a)/(b) tell. These stay:
`activities/activity.go:63`, `user_task.go:113`; `data/item.go:101,261`,
`association.go:105`; `flow/sequenceflow.go:111`; `gateways/gateway.go:128`;
`process/process.go:70`; `events/start.go:77`, `end.go:72`;
`consinp/consinp.go:94`.

### §2.4 The one polymorphic consumer

`foundation.NewBaseElement` (base.go:98) loops `for _, o := range opts {
o.Apply(&bc) }` with **no** switch — the only place `Apply` is real polymorphic
dispatch. It only ever receives `BaseOption` (every other constructor collects
just `BaseOption` into `baseOpts` before delegating here). It also currently
serves as the base-layer catch-all: options that leaked through a
constructor without a category-(b) guard reach here and are rejected when
`BaseOption.Apply(*baseConfig)` fails. Hardening it to a switch must add an
explicit `default:` to preserve that rejection (§6.1).

---

## §3 Solution

Harden every constructor to call the option's concrete func directly, delete
all 21 `Apply` methods (removing every dead assertion + `reflect`), and
redefine `options.Option` as a marker interface so `...options.Option` stays
type-safe without a generic `Apply`. Category-(b) validation is fully
preserved.

### §3.1 Alternatives considered

| Alternative | Pros | Cons | Decision |
|---|---|---|---|
| A. Harden dispatch only, keep `Apply` (Depth 1) | no interface change; dead branches stop executing (no runtime reflection) | leaves 21 orphaned `Apply` methods — the exact dead-code smell being removed | ❌ rejected |
| B. Keep `Apply`, only drop the `reflect.TypeOf(cfg)` detail | minimal diff; reflection-free | keeps the dead assertion + redundant indirection | ❌ rejected: half-measure |
| C. **Full: harden dispatch + delete `Apply` + marker `Option` (Depth 2)** | removes all dead assertions, all option-layer reflection, and the redundant round trip; compile-time-safe option bag | wider mechanical churn (~21 files + interface + ~10 tests); marker must be exported (§3.1 note) | ✅ chosen |

**Cross-package marker constraint (why the marker is exported):** Go only
permits a type to implement an interface's **unexported** method within the
defining package. The 21 option types live in 7 packages, so a sealed
`interface { isOption() }` is impossible. The marker is therefore an
**exported** no-op method (`Option()`). It's not perfectly sealed (an external
type could implement it) but constrains `...options.Option` to real option
types at compile time — which is the goal; sealing against out-of-tree
imposters is a non-concern for a gobpm-internal utility.

### §3.2 Changes by file

#### §3.2.1 `pkg/model/options/option.go` — marker interface

```go
// before:
type Option interface { Apply(cfg Configurator) error }
// after:
type Option interface { Option() }  // marker; options apply via constructor dispatch
```

Assess `Configurator` (`interface { Validate() error }`): if still used by
config self-validation (`cfg.Validate()` in constructors) it stays; if only
`Apply`'s signature referenced it, it's removed. (Determined at implementation
by grep; recorded in §8.)

#### §3.2.2 The 21 option types (§2.2 inventory) — delete `Apply`, add marker

Per type, mechanically: delete the `Apply` method (with its dead assertion +
reflect), add `func (X) Option() {}`, drop the now-unused `reflect` import.
Uniform transformation; the inventory table is the complete file list.

#### §3.2.3 The dispatching constructors — direct func dispatch

Split each multi-type switch case into per-type cases that call the option
func directly; convert single-case and concrete-assertion sites likewise.
**Preserve every category-(b) `default:` guard.**

- Multi-type: `events` start (start.go:73), `events` end (end.go:68),
  `newActivity` (activity.go:56), `flow` newSequenceFlow (sequenceflow.go:104),
  `process.New` (process.go:63), `gateways.New` (gateway.go:120).
- Single-case: `newTask` (task.go:36), user-task (user_task.go:105),
  `NewItemDefinition` (item.go:97), `NewIAE` (item.go:257), `NewAssociation`
  (association.go:98), `NewRenderer` (consinp.go:82).
- Concrete-assertion: `newEvent` (event.go:171), `NewProp` (property.go:105),
  `NewComplexGateway` (complex.go:210), `NewEventBasedGateway`
  (event_based.go:134).

Example (`newActivity`):
```go
// before:
case ActivityOption, RoleOption, data.PropertyOption:
	if err := o.Apply(&cfg); err != nil { ee = append(ee, err) }
// after:
case ActivityOption:
	if err := o(&cfg); err != nil { ee = append(ee, err) }
case RoleOption:      // *activityConfig implements RoleConfigurator
	if err := o(&cfg); err != nil { ee = append(ee, err) }
case data.PropertyOption:  // *activityConfig implements PropertyAdder
	if err := o(&cfg); err != nil { ee = append(ee, err) }
```

#### §3.2.4 `pkg/model/foundation/base.go` — switch-harden `NewBaseElement`

Convert the polymorphic `for … { o.Apply(&bc) }` loop to
`switch o := opt.(type) { case BaseOption: bo(&bc); default: <reject> }`. The
new explicit `default:` replaces the base-layer catch-all that
`BaseOption.Apply`'s dead assertion currently provided (§2.4, §6.1).

#### §3.2.5 Test call sites — update `.Apply(...)`

~10 sites call `.Apply(...)` directly: `property_test.go:87-128`,
`options_test.go:51-64`, `base_test.go:60`, `complex_test.go:113`,
`event_based_test.go:229`, and the `receive/send` internal tests. Update to
call the underlying func (or assert the marker). Enumerated exactly at
implementation.

---

## §4 Verification

Coverage: this is a behavior-preserving refactor — the existing constructor
tests are the regression net.

### §4.1 Regression tests

#### §4.1.1 Behavior unchanged — existing constructor suites pass

**Existing:** every `New*` constructor test across `pkg/model` (activities,
events, data, gateways, flow, foundation, process, consinp). A valid option set
must build the same object; assertions on the built object are unchanged.

#### §4.1.2 Category-(b) misuse still rejected

**Existing / augment:** passing an option a constructor doesn't accept still
returns an "invalid option type" error (the (b) guards). Where a package lacks
such a test, add one (e.g. `NewServiceTask("x", op, <a gateway option>)` →
error).

#### §4.1.3 Compile-time safety of the marker

`New("x", 42)` (a non-option) must **fail to compile** under the marker
interface. Not a runtime test — noted as a property; verified by the
`go build ./...` gate (an accidental `...Option` = `...any` regression would
let non-options through).

### §4.5 Observability

None — no behavior change; the win is dead-code + reflection removal, visible
in the diff and a `grep -rn "reflect" pkg/model` going quiet on the option
layer.

---

## §5 Prevention

- **Doc comment on `options.Option`**: it is a compile-time marker; options are
  applied by each constructor's type-switch calling the option's concrete func
  — there is no generic `Apply`. New option types add the marker + their func;
  new constructors dispatch by direct func call (follow `NewReceiveTask` /
  `NewServiceTask`), never reintroduce an `Apply(Configurator)` round trip.
- **Canary:** the category-(b) misuse tests (§4.1.2) + the `go build` marker
  constraint (§4.1.3).

---

## §6 Regressions / side-effects

### §6.1 What may rely on the old behaviour

- **`foundation.NewBaseElement` base-layer rejection** — options that leaked
  past a guardless constructor were caught here by `BaseOption.Apply`'s failure.
  The switch-hardening MUST add an explicit `default:` reject to preserve it
  (§3.2.4). Audit: `grep -rn "NewBaseElement\|baseOpts" pkg/model` to confirm no
  path depends on the old silent-collect-then-reject flow.
- **Any external caller of `options.Option.Apply`** — grep `grep -rn "\.Apply(" pkg internal`
  confirmed all call sites are in-tree constructors/tests; deleting `Apply`
  breaks nothing outside those (updated in §3.2.3/§3.2.5).
- **`Configurator`** — if it becomes unused after `Apply` deletion, remove it;
  if config self-validation still uses it, keep. Confirm pre-landing.

### §6.2 Rollback path

Single-commit revert (or a small stack of stage commits). No data/migration.

### §6.3 Cross-team backlog

None — `pkg/model/options` and its consumers are all in-repo.

---

## §7 Related

- **[FIX-019]** (string-typed reflection-free `errs` details) — surfaced these
  `reflect.TypeOf(cfg)` sites during its call-site migration; FIX-019
  deliberately left them for this FIX.
- **Precedent:** the `eventConfig` fold (this session) — folded per-adder
  assertions into the `eventConfig` interface so `WithXxxTrigger` calls
  `cfg.setXxx` directly. Same "compile-enforce, delete the runtime assertion"
  move, one layer down. FIX-020 completes it at the `options.Option` layer.
- **Reference implementations:** `NewReceiveTask` / `NewSendTask` already use
  direct func dispatch — the target pattern.
- **Promote-to-ADR candidate:** the invariant "model options apply via
  constructor type-switch + direct func call; `options.Option` is a marker, not
  a generic applicator" could promote to a small ADR if a third options-related
  decision appears.

---

## §8 Implementation summary (stage-by-stage actual landings + deltas vs draft)

### §8.1 Stages by commit (branch `test/harden-core-coverage`)
| Stage | Commit | Scope | Tests |
|---|---|---|---|
| 1 | `8aacd72` | marker `Option()` interface; deleted all 21 `Apply` methods; hardened ~18 constructors + `NewBaseElement` (switch + explicit reject); updated ~10 `.Apply` test call sites — landed atomically (the interface change ripples through all of it) | constructor suites unchanged; net −179 lines |
| — | `8bbfa46` | error-path tests (see §8.2) | instance/thresher/eventhub |

The 3 draft stages could not be separately-green: changing `options.Option` to
the marker breaks every constructor that still calls `.Apply` until they all
switch to direct dispatch, so it lands as one commit.

### §8.2 Empirical findings — where reality diverged from the §3 draft
- **`Configurator` kept.** After deleting the `Apply` signatures, `Configurator`
  is still referenced (embedded by `NameConfigurator`/`RoleConfigurator`/
  `PropertyAdder`/`IAEAdder`/`eventConfig`; each config's `Validate()` is called
  by its constructor), so it stays.
- **All 5 interface-typed options compiled directly** — no config failed to
  implement the required interface; the hardening had no exceptions.
- **`errorClass` orphaned in `options`.** Deleting `NameOption.Apply` left
  `options.errorClass` unused (golangci-lint `unused`); removed it. (No other
  package's `errorClass` was affected — all still used by real errors.)
- **Diff-coverage detour (`8bbfa46`).** The branch-wide diff-coverage gate
  (measured vs `origin/master`, so it spans FIX-019+FIX-020) flagged 7 FIX-019
  diagnostic-detail lines in error branches that were only *flakily* covered by
  integration tests (FIX-019's own gate got lucky). Added deterministic unit
  tests for `instance.Run`/`PropagateEvent` guards, `thresher.PropagateEvent`'s
  hub-failure, and `eventhub`'s `Process`/`AddEventProcessor` failures → 96.8%.

### §8.3 Backlog (out of FIX-020 scope)
- Category-(b) constructor guards still reflect on the misused option
  (`reflect.TypeOf(o).String()`); left in place (reachable input validation on a
  cold misuse path, not a runtime hot path). De-reflecting them is a possible
  follow-up.

[FIX-019]: FIX-019-errs-string-typed-details.md
