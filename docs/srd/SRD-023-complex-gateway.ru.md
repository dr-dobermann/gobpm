# SRD-023 — Complex gateway (синхронизирующее слияние, управляемое активацией)

| Поле | Значение |
|---|---|
| Статус | Принято |
| Версия | v.1 |
| Дата | 2026-06-20 |
| Владелец | Руслан Габитов |
| Реализует | [ADR-005 v.3 Gateways & Joins](../design/ADR-005-gateways-and-joins.ru.md) §2.11 |

Этот SRD приземляет **Complex gateway**, решённый в [ADR-005 v.3](../design/ADR-005-gateways-and-joins.ru.md)
§2.11: **сходящееся** синхронизирующее слияние, завершение которого — это **правило
активации** (дизъюнкция троек `(condition, count, requiredFlows)`) — и **расходящееся**
разделение, переиспользующее Inclusive split (§2.9). Он целиком переиспользует машинерию
park/resume + достижимости OR-join'а ([SRD-022](SRD-022-inclusive-or-join.ru.md), вбок),
меняя только правило завершения и добавляя путь оценки data-guard'ов.

---

## 1. Background

ADR-005 v.3 §2.11 добавляет Complex gateway как явное расширение поверх Common
Executable Subclass (`bpmn-spec/conformance.md`), для шаблонов Discriminator (WCP-9/28)
и Partial-Join (WCP-30/31). Сходящийся шлюз — это синхронизирующее слияние, поэтому
он строится прямо на том, что уже есть в дереве:

- Машинерия park/resume + death-trigger OR-join'а: `internal/instance/track.go`
  `synchronize` (`track.go:491`) паркует приход reachability-join'а на `TrackAwaitSync`
  + `parkCh`, а цикл перепроверяет при приходе/гибели (`instance.go:767`
  `recheckAwaitingJoins`, `instance.go:804` `recheckJoin`, `instance.go:829`
  `fireOrJoin`, `instance.go:789` `hasInTransitArrival`).
- Оракул достижимости: `internal/instance/reachability.go:15` `CheckFlows`
  (обратный, игнорирующий условия, защищённый от циклов) по `occupiedNodes` (`reachability.go:43`).
- Модельный паттерн: `pkg/model/gateways/inclusive.go` (`InclusiveGateway`,
  `var _ exec.ReachabilityJoin`, `Arrive`/`Recheck`/`unmarkedFlows`/`absorb`/`Clone`).
- Разделение: `inclusive.go:89` `Exec` форкует условно-истинное подмножество (§2.9), а
  `gateway.go:207` `checkCondition` оценивает `data.FormalExpression` через
  `re.ExpressionEngine().Evaluate`.

**Пробел.** Complex gateway не существует. Его правило завершения отличается от OR-join'а
двумя способами, которые текущие контракты не обслуживают:

1. **Data guards.** Каждая тройка несёт опциональное `condition` над **process data**.
   Условия сегодня оцениваются только во время `Exec` (с `renv.RuntimeEnvironment`,
   построенным из per-node frame, `execenv.go:21` `newExecEnv`). Решение join'а
   происходит раньше — при `Arrive` (`track.go:502` — **нет frame, нет `re` ещё**) и при
   `Recheck` (`instance.go:819` — цикл передаёт только `fc`, **нет `re`**). Поэтому
   data-guard'ам нужен новый, frame-free канал оценки.
2. **Гибель означает abort, а не fire.** OR-join срабатывает по death-trigger'у (гибель
   может сделать непомеченный поток недостижимым → completion). Счётчик Complex gateway
   **монотонен** — гибель никогда не добавляет приход — так что гибель может только сделать
   тройку *невыполнимой*. Поэтому путь гибели **abort'ит** (throws), а не fire'ит.

Этот SRD добавляет Complex-специфичный контракт, модельный шлюз, обвязку track/loop,
validation-хук на момент регистрации, тесты и пример.

---

## 2. Requirements

### Functional

- **FR-1 — модельный тип `ComplexGateway`.** Новый `pkg/model/gateways/complex.go`
  `ComplexGateway`, встраивающий `Gateway` (зеркалит `InclusiveGateway`, `inclusive.go:25`),
  несущий своё правило активации + per-instance состояние прихода (`arrived`, `order`,
  `fired`) под собственным `mu`. `Clone()` (свежее состояние прихода, ADR-009), `Node()`.
- **FR-2 — тройки активации.** Правило — это `[]activationTriple`, каждая
  `{ cond data.FormalExpression; count int; required []string }` (id входящих потоков).
  Конструкторы: `WithActivationThreshold(n int)` (sugar для одной тройки `{nil, n, nil}`)
  и `WithActivation(...activationTriple)` через публичный triple builder. Эти два
  **взаимоисключающи** и **хотя бы один** обязателен → иначе ошибка построения
  (validate-all-params).
- **FR-3 — расходящееся разделение.** Расходящийся, `Exec` форкует условно-истинное
  исходящее подмножество ровно как Inclusive split (§2.9) — переиспользует `checkCondition`
  + правила default/exception (`inclusive.go:89`). У сходящегося/`≤1`-исходящего Complex
  gateway `Exec` — это post-merge продолжение выжившего.
- **FR-4 — контракт `exec.ActivationJoin`.** Новый интерфейс в `pkg/exec/exec.go`:
  сходящийся Complex gateway записывает приходы и решает fire/park/abort, используя
  переданный вызывающим **`GuardEval`** (оцениватель data-guard'ов) и существующий
  `FlowChecker` (достижимость). См. §3.3.
- **FR-5 — правило срабатывания.** При приходе шлюз срабатывает, когда **некоторая тройка
  выполнена**: `eval(cond)` истинно (или `cond == nil`) **и** `|arrived| ≥ count`
  **и** `required ⊆ arrived`. Завершающий приход — это выживший (последний пришедший);
  остальные пришедшие track'и сливаются. Переиспользует `absorb` (`inclusive.go:216`).
- **FR-6 — park.** Незавершающий приход паркуется: `TrackAwaitSync` + `evParked` +
  блокировка на `parkCh` — **тот же** путь, что использует OR-join (`track.go` synchronize),
  расширенный, чтобы распознавать `ActivationJoin`.
- **FR-7 — abort (anti-hang).** При гибели токена (и при незавершающем приходе) шлюз
  abort'ит — **фейлит instance** — когда **каждая** тройка **мертва**:
  `|arrived| + |reachable| < count`, **или** `required`-gate не среди пришедших и не в
  `reachable` (обязательный gate никогда не придёт). `reachable` — это `CheckFlows` по
  непомеченным входящим потокам. Точно (счётчики монотонны; достижимость gate структурна).
- **FR-8 — exhaustion no-match.** Когда больше токенов прийти не может (`reachable` пуст)
  и guard ни одной тройки не держится при максимальных счётчиках, шлюз бросает "arrivals
  exhausted, no activation matched" — аналог Exclusive no-match (§2.8).
- **FR-9 — trailing tokens.** После срабатывания более поздний приход по другому входящему
  потоку поглощается (track заканчивается на шлюзе); шлюз не перевзводится (single-pass).
- **FR-10 — канал оценки guard'ов.** Instance выставляет `GuardEval`, построенный поверх
  его **root** data scope (`inst.dataPlane.Root()`, `instance.go:494`) +
  `inst.ExpressionEngine()` — frame-free, на уровне процесса. И track (`t.instance`), и
  цикл получают его; никакой node-execution frame не фабрикуется.
- **FR-11 — валидация при регистрации.** Per-node validation-хук в `Process.Validate`
  (`process.go:213`) вызывает опциональный `interface{ Validate() error }` на каждом узле;
  `ComplexGateway` реализует его, чтобы проверить относительно своих уже-связанных входящих
  потоков: `1 ≤ count ≤ M`, `count ≥ len(required)`, и каждый `required` id — реальный
  входящий поток. Build-time-проверки (`count ≥ 1`, `count ≥ len(required)`, ≥1 тройка)
  остаются в конструкторе; `count ≤ M` + членство id — на момент регистрации.

### Non-functional

- **NFR-1 — изоляция namespace.** `count`'ы активации и идентичности gate'ов никогда не
  попадают в data namespace; `condition` — обычное process-data выражение. Никаких
  зарезервированных имён переменных, никаких префиксов (ADR-005 v.3 §2.11 Engine note).
- **NFR-2 — конкурентность.** Состояние прихода — per-node, per-instance (ADR-009),
  мутируется под собственным `mu` шлюза; конкурентные приходы атомарны (ADR-005 §2.4).
  `GuardEval` читает закоммиченный root scope; никаких write race'ов.
- **NFR-3 — Parallel/OR не затронуты.** `ParallelGateway` и `InclusiveGateway` сохраняют
  свои контракты; новый путь `ActivationJoin` аддитивен в `synchronize` / `recheckJoin`.
- **NFR-4 — покрытие.** Затронутые файлы финишируют с ≥95% diff-coverage (`make ci`
  `cover-check`), цель 100%.

---

## 3. Models

### 3.1 `ComplexGateway` (`pkg/model/gateways/complex.go`)

```go
// activationTriple is one disjunct of a Complex gateway's activation rule: the join
// fires when cond holds (nil = always), count incoming flows have arrived, and every
// required incoming flow is among them.
type activationTriple struct {
    cond     data.FormalExpression // optional process-data guard
    count    int                   // total arrivals required
    required []string              // incoming-flow ids that must be among the arrived
}

type ComplexGateway struct {
    activation []activationTriple
    order      []string          // arrival order (survivor selection)
    arrived    map[string]string // incomingFlowID -> arrivingTrackID
    Gateway
    mu    sync.Mutex
    fired bool
}

var (
    _ exec.NodeExecutor   = (*ComplexGateway)(nil)
    _ exec.ActivationJoin = (*ComplexGateway)(nil)
)
```

Зеркалит `InclusiveGateway` (`inclusive.go:25`): та же форма `arrived`/`order`/`fired`,
тот же `Clone` (свежее состояние) и аксессоры `Node`.

### 3.2 Конструктор + опции (соседи `gateway_options.go`)

```go
func NewComplexGateway(opts ...options.Option) (*ComplexGateway, error)

// WithActivationThreshold / WithActivation are Complex-specific options (a
// ComplexOption type) sorted by NewComplexGateway the way New() type-switches
// GatewayOption vs foundation.BaseOption (gateway.go:98); name/direction/id pass
// straight to the embedded Gateway via New().
func WithActivationThreshold(n int) ComplexOption // one guard-less triple {nil, n, nil}
func WithActivation(triples ...Triple) ComplexOption

// Triple is a public builder for one activation disjunct.
func NewTriple(count int, opts ...TripleOption) (Triple, error) // WithGuard, WithRequired
```

`NewComplexGateway` сортирует свои опции (base/name/direction → `New(...)`; активация →
эквивалент gatewayConfig), затем применяет build-time-валидацию (зеркалит конвенцию
self-naming option-конструкторов): отвергает оба источника активации, отвергает ноль
троек, отвергает `count < 1`, отвергает `count < len(required)` — каждое с
самоидентифицирующим `errs`-сообщением.

### 3.3 `exec.ActivationJoin` (`pkg/exec/exec.go`)

```go
// GuardEval evaluates a Complex gateway's data guard against process-level data.
// Supplied by the instance (root scope + expression engine); a nil cond is true.
type GuardEval func(cond data.FormalExpression) (bool, error)

// ActivationJoin is a converging gateway whose completion is an activation rule over
// per-triple data guards, arrival counts, and required gates (ADR-005 v.3 §2.11). It
// reuses the reachability machinery (FlowChecker) but, unlike a ReachabilityJoin, a
// token death makes it ABORT (the count is monotonic) rather than fire.
type ActivationJoin interface {
    NodeExecutor

    // Record registers arrivingTrackID's arrival on incomingFlowID and reports
    // whether the gateway already fired (the arrival is then a trailing token to
    // consume). It makes NO activation decision — reachability + guards are read only
    // by the loop (Recheck), never off the arriving track's goroutine, because the
    // live-token set CheckFlows reads is loop-owned and must not be raced.
    Record(incomingFlowID, arrivingTrackID string) (firedAlready bool)

    // Recheck is the loop's decision: fire (survivor + merged), abort (the rule is
    // unsatisfiable), or wait. Run after an arrival parks and on every token death.
    Recheck(eval GuardEval, fc FlowChecker) (Decision, error)
}

type Decision struct {
    Fired    bool
    Aborted  bool
    Survivor string
    Merged   []string
}
```

`ParallelGateway` (`SynchronizingJoin`) и `InclusiveGateway` (`ReachabilityJoin`)
не изменяются; `ActivationJoin` — родственник, распознаваемый аддитивно в точках вызова.

### 3.4 Per-node validation-хук (`pkg/model/process/process.go`)

```go
// in Process.Validate, after the flow-connectivity pass:
for _, n := range p.nodes {
    if v, ok := n.(interface{ Validate() error }); ok {
        if err := v.Validate(); err != nil { ee = append(ee, err) }
    }
}
```

`ComplexGateway.Validate()` проверяет `1 ≤ count ≤ len(Incoming())`,
`count ≥ len(required)` и `required ⊆ Incoming()` для каждой тройки.

---

## 4. Analysis

### 4.1 Почему callback `GuardEval`, а не протянутый `re`

Ground truth: при `Arrive` (`track.go:502`) пришедший track **не построил свой execution
frame** (frame'ы создаются в `executeNode`, после `synchronize`), а при `Recheck`
(`instance.go:819`) цикл передаёт только `fc` и **не исполняет одиночный узел** — нет
per-execution frame и нет очевидного «чьего токена data view». `data.FormalExpression`
оценивается через `re.ExpressionEngine().Evaluate(ctx, cond, re)` (`gateway.go:207`), а
`re` — это `execEnv{Instance, *scope.Frame}` (`execenv.go:21`).

Complex-guard'ы читают данные **уровня процесса** (properties), которые живут в **root**
scope instance'а, а не в node-local frame. Поэтому instance может построить frame-free
оцениватель: `newExecEnv(inst, inst.dataPlane.Root())` → `RuntimeEnvironment` для чтений
уровня процесса. Мы выставляем это как замыкание `GuardEval` и передаём его в **`Recheck`**
(решение цикла) — `Record` track'а не берёт оцениватель, потому что и достижимость, **и**
guard'ы читаются только циклом, никогда не из goroutine пришедшего track'а (live-token set,
который читает `CheckFlows`, принадлежит циклу и не должен попадать в race; доказано
`-race`). Шлюз сохраняет владение решением (он знает свои тройки); цикл поставляет
способность. Это избегает фабрикации node frame (архитектурно неверной) и избегает помещения
счётчиков в namespace (NFR-1).

**Рассмотренная альтернатива — протянуть `renv.RuntimeEnvironment` через `Arrive`/`Recheck`.**
Отвергнута: вынуждает per-node frame в точках, где его нет, и связывает контракт join'а с
полной runtime-поверхностью ради единственной способности (оценки guard'ов). Callback — это
минимальный канал.

**Рассмотренная альтернатива — instance предоценивает guard'ы и передаёт bitset.**
Отвергнута: инвертирует владение (instance'у пришлось бы знать тройки шлюза) и переоценивает
жадно даже когда структурная часть не может сработать.

### 4.2 Почему гибель abort'ит (а не fire'ит) — расхождение с `ReachabilityJoin`

`Recheck` OR-join'а срабатывает при гибели (гибель может сделать последний непомеченный
поток недостижимым → «все достижимые пришли» → completion). Счётчик Complex — это
**монотонный порог `≥`**: гибель никогда не увеличивает `|arrived|`, так что она может только
толкнуть тройку из *maybe* в *dead*. Поэтому путь гибели Complex вычисляет **abort**, никогда
fire. Вот почему `ActivationJoin` — отдельный контракт, а не переиспользование
`ReachabilityJoin` — у ветви `Decision.Aborted` нет аналога в OR-join'е. Срабатывание решает
`Recheck` цикла (приход записывает, затем паркуется, а recheck цикла его fire'ит); гибель
лишь когда-либо abort'ит. Тест abort/exhaustion переиспользует `CheckFlows` без изменений.

### 4.3 Почему per-node `Validate`-хук

`Process.Validate` (`process.go:213`) сегодня проверяет только связность потоков; нет
per-node validation-прохода (подтверждено: нет цикла по `p.nodes`, вызывающего node
`Validate`). `count ≤ M` и `required ⊆ incoming` познаваемы только после связывания, т.е. на
момент регистрации (`snapshot.New` → `p.Validate`, `thresher.go` `RegisterProcess`).
Добавление опционального хука `interface{ Validate() error }` — наименее инвазивное место и
переиспользуемо другими узлами позже. Познаваемые на build-time проверки (`count ≥ 1`,
`count ≥ len(required)`, ≥1 тройка) остаются в конструкторе.

### 4.4 Обвязка track/loop (переиспользование + аддитивная ветвь)

`synchronize` (`track.go`) получает ветвь `ActivationJoin` **перед** проверкой
`SynchronizingJoin` (Complex — не `SynchronizingJoin`): `synchronizeActivation` вызывает
`Record(flowID, trackID)` и либо **поглощает** trailing token (шлюз уже сработал), либо
**паркует** (переиспользуя `TrackAwaitSync`/`parkCh`/`evParked`) — track **не** принимает
решения. `recheckJoin` (`instance.go`) получает ветвь `ActivationJoin` через type-switch:
`Recheck(inst.guardEval(ctx), inst)` → `Fired` → `fireOrJoin` (возобновляет припаркованного
выжившего); `Aborted` → `fail` instance'а (lastErr + cancel, loop-only single writer); иначе
ждёт. Guard `hasInTransitArrival` применяется без изменений.

---

## 5. Public API / contract surface

- `gateways.NewComplexGateway(opts...) (*ComplexGateway, error)` — `foundation.WithID`,
  `foundation.WithDoc`, `options.WithName`, `gateways.WithDirection`, плюс
  `WithActivationThreshold(n)` **xor** `WithActivation(triples...)`.
- `gateways.NewTriple(count, WithGuard(expr), WithRequired(flowIDs...))`.
- `exec.ActivationJoin`, `exec.GuardEval`, `exec.Decision` (новый публичный контракт).
- Никаких изменений в `SynchronizingJoin` / `ReachabilityJoin` / `FlowChecker`.

---

## 6. Test scenarios

**Model-unit** (`pkg/model/gateways/complex_test.go`, рукописные заглушки `FlowChecker` +
`GuardEval`): `TestNewTriple` / `TestNewComplexGateway` (build-валидация +
threshold-xor-expression взаимоисключение); `TestComplexFiresAtThreshold` (Record затем
Recheck срабатывает на пороге, выживший — последний пришедший); `TestComplexGuardGatesFire`
(guard gate'ит срабатывание); `TestComplexRequiredFires` (required gate);
`TestComplexAbortCountUnreachable` / `TestComplexAbortRequiredUnreachable` /
`TestComplexExhaustionNoMatch` (пути abort'а); `TestComplexRecheckReachabilityError`
/ `TestComplexGuardError` (консервативное ожидание / проброс guard-error);
`TestComplexRecheckNoArrivals`; `TestComplexOptionApplyWrongConfig`;
`TestComplexIsActivationJoin`; `TestComplexValidate`; `TestComplexGatewayClone`;
`TestComplexSplitSubset`.

**In-package** (`internal/instance/complex_internal_test.go`, прогоняет diamond на реальном
instance, чтобы обвязка записалась per-package coverage-профилем):
`TestComplexDiscriminatorInstance`, `TestComplexGuardInstance`,
`TestComplexAbortInstance`, `TestComplexGuardEvalErrorInstance`,
`TestComplexGuardNotBoolInstance`.

**Registration** (`pkg/model/process/process_test.go`):
`TestProcessValidateComplexGateway` — out-of-range порог отвергается при регистрации;
валидный проходит; узлы без метода `Validate()` не затрагиваются.

**Engine** (`pkg/thresher/complex_gateway_test.go`, `-race`): `TestComplexDiscriminator`
(1-of-3, остальные поглощены), `TestComplexPartialJoin` (2-of-3, 3-й поглощён),
`TestComplexDataAware` (amount выбирает порог), `TestComplexRequiredGate`,
`TestComplexAbortOnDeath` (отведённая ветвь гибнет → death-recheck abort'ит, без зависания).

**Example** (`examples/complex-gateway/`): 3-approver data-aware partial join
(`process.go` + `main.go`, entry ≤80 строк), smoke exit 0.

---

## 7. Worked example (data-aware partial join)

```
start ─OR┬→ manager ─┐
         ├→ finance ─┤ Complex join: [(amount<1000, 2), (amount>=1000, 3[cfo])]
         └→ cfo ─────┘        → finalize → end
```

`amount = 500` → двух из {manager, finance, cfo} достаточно → срабатывает на 2-м приходе.
`amount = 5000` → нужны 3 **включая cfo**; срабатывает только когда cfo + двое других внутри;
если ветвь cfo гибнет первой, death-recheck abort'ит (без тихого зависания).

---

## 8. Cross-document references

- **Реализует** [ADR-005 v.3](../design/ADR-005-gateways-and-joins.ru.md) §2.11 (решение
  Complex gateway); §2.9 (переиспользование split'а), §2.10 (park/resume + достижимость),
  §2.4 (владение синхронизацией).
- Уточняет pin [ADR-001 v.5](../design/ADR-001-execution-model.ru.md) (tracks/tokens/loop),
  [ADR-009 v.1](../design/ADR-009-per-instance-node-graph.ru.md) (per-instance node state).
- Вбок [SRD-022 v.1](SRD-022-inclusive-or-join.ru.md) — переиспользуемая машинерия OR-join'а.

(Только вверх/вбок; никаких ссылок вниз.)

---

## 9. Definition of Done

- FR-1…FR-11 подключены (модельный тип, тройки, split, `ActivationJoin`, fire/park/abort/
  exhaustion/trailing, канал guard'ов, валидация при регистрации).
- §6 model-unit + engine-тесты присутствуют и зелёные под `-race`.
- `examples/complex-gateway` smoke exit 0; бинарь в gitignore.
- `make ci` зелёный: lint, build, `-race`, **diff-coverage ≥95%** на затронутых файлах
  (цель 100%), govulncheck.
- ADR-005 v.3 §2.11 удовлетворён; NFR-1…NFR-4 держатся (изоляция namespace, конкурентность,
  Parallel/OR не затронуты, покрытие).
- `/check-srd` PASS; затем flip Accepted + RU twin + ADR-005 v.3 → Accepted (sync linked
  docs).

## 10. Implementation summary

Приземлён на `feat/complex-gateway` (от `master`): четыре milestone'а + документ.

### 10.1 Commits

| M | Commit | Scope |
|---|---|---|
| doc | `b6d3da9` | SRD-023 draft |
| M1 | `b344795` | `exec.ActivationJoin` + `ComplexGateway` model + model-unit tests |
| M2 | `a956d76` | per-node `Process.Validate` hook |
| M3 | `a632602` | instance wiring (Record/Recheck, guardEval, recheckJoin, synchronizeActivation) |
| M4 | `55d02f7` | `examples/complex-gateway` + in-package coverage tests |

Смежное в том же PR: debug-level event logging (`5cdbd52`) и **FIX-006**
(`6dcd370`) — зависание OR-join'а при приходе всех веток всплыло при сборке M3.

### 10.2 Key files

- `pkg/exec/exec.go` — `ActivationJoin` (`Record` + `Recheck`), `GuardEval`, `Decision`.
- `pkg/model/gateways/complex.go` — `ComplexGateway`, `Triple`, опции, `Exec` (через
  `forkTrueSubset`), `Record`, `Recheck`, `decide`, `evalTriple`, `Validate`.
- `pkg/model/gateways/gateway.go` — `forkTrueSubset` (общий split §2.9).
- `pkg/model/process/process.go` — per-node `Validate`-хук.
- `internal/instance/activation.go` — `guardEval`, `fail`.
- `internal/instance/track.go` — `synchronizeActivation`.
- `internal/instance/instance.go` — ветвь `ActivationJoin` в `recheckJoin`.
- `examples/complex-gateway/`.

### 10.3 Verification

- `make ci` зелёный: lint, build, `-race`, diff-coverage **97.4%** (≥95), govulncheck.
- Тесты: model-unit (Record/Recheck), in-package (`internal/instance`), registration
  (`Process.Validate`) и engine (`-race`): discriminator, partial-join, data-aware,
  required-gate, abort-on-death.
- `examples/complex-gateway` smoke exit 0; все 13 примеров exit 0.

### 10.4 Deltas vs the draft

- **`Activate` → `Record` + loop `Recheck`.** В драфте track вызывал
  `Activate(eval, fc)` и решал. Но `CheckFlows` — **loop-only** (чтение из track'а
  гонится с `inst.tracks`, доказано `-race`). Поэтому track только **Record**'ит; цикл
  владеет всем решением fire/abort через `Recheck`. §3.3 + §4 это отражают.
- **Trailing tokens через `Record` → `firedAlready`** — post-fire consume; тот же
  паттерн был отзеркален в OR-join через FIX-006.
- Имена §6-тестов устаканились во время приземления; §6/§10.2 перечисляют реальные имена.

## Открытые вопросы

- **Нет.**
