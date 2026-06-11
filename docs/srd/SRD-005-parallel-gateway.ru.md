# SRD-005 — Parallel Gateway (расхождение + синхронизирующий AND-join)

| Поле | Значение |
|---|---|
| Статус | Принято |
| Версия | v.1 |
| Дата | 2026-06-09 |
| Владелец | Руслан Габитов |
| Реализует | [ADR-005 v.1 Gateways & Joins](../design/ADR-005-gateways-and-joins.md) |
| Уточняет | [ADR-001 v.5 Execution Model](../design/ADR-001-execution-model.md); [ADR-009 v.1 Per-instance node graph](../design/ADR-009-per-instance-node-graph.md) |

> EN-оригинал — канонический: [SRD-005-parallel-gateway.md](SRD-005-parallel-gateway.md). Этот файл — его перевод (twin).

Этот SRD приземляет **Parallel (AND) шлюз** из [ADR-005](../design/ADR-005-gateways-and-joins.md): расходящееся **расхождение** (активировать все исходящие) и сходящийся **синхронизирующий join** (дождаться по одному токену на каждом входящем flow, затем продолжить на одном выжившем track). Это пилот, который строит машинерию синхронизирующего join'а (узел join на каждый instance владеет своим набором прибытий, который пишет event-loop Instance — ADR-009 v.1; продолжение «прокатиться на прибывшем track»; продьюсер `TrackMerged`), которую переиспользуют Inclusive/Complex join'ы. Он также приземляет **упрощение контракта исполнения узла**, которое предписывает ADR-005 §2.5 — схлопывание до единственного `Execute` с удалением ставших избыточными хуков prologue/epilogue. Inclusive (OR), Complex и Event-Based шлюзы — **вне области** (ADR-005 §4).

## 1. Предпосылки и мотивация

### 1.1 Текущее состояние (baseline до приземления)

> Это **отправной** снимок, мотивировавший работу (снят относительно master до приземления этого SRD); ссылки file:line описывают состояние *до*. Перепроверено на M1 относительно baseline графа узлов на каждый instance из ADR-009/SRD-006. То, что он описывает — краш на Parallel-узле, отсутствующий учёт join'а, продьюсерлесс `TrackMerged`, off-by-one `String()`, остаток prologue/epilogue — это ровно то, что этот SRD убирает; приземлённый результат см. в §7.

- Исполняется только **Exclusive** шлюз (`pkg/model/gateways/exclusive.go:53` `Exec`). Диспетчеризация идёт по конкретному типу через `Exec` посредством `exec.NodeExecutor` (`internal/exec/exec.go:10-18`); узел без него обрывает создание track (`internal/instance/track.go:231-236`, `:419-425`). **Parallel-шлюз в модели сегодня крашится там.**
- Механика расхождения существует: `Exec` узла возвращает активированные исходящие flow; track продолжается на первом, эмитит `evFork` для остальных, по одному новому track на каждый (`track.go:508-547` `checkFlows`, `instance.go:340-356` `case evFork`, `event.go:8-26`).
- **Учёта join'а нет**: ничто не читает `Incoming()` узла, чтобы решить, ждать ли (`ADR-005 §1`). `BaseNode.Incoming()/Outgoing()` доступны на любом узле (`pkg/model/flow/node.go:118-125`).
- `TrackMerged` (`internal/instance/track.go:81`) существует и уже проецируется в `TokenConsumed` (`internal/instance/token.go:86-90`) — но **не имеет продьюсера**.
- `trackState.String()` (`track.go:91-104`) **off-by-one**: он перечисляет фантомный `"TrackWaitForInteraction"` без соответствующей const, поэтому каждое состояние с индекса 5 (`TrackMerged`) и далее печатает неправильное имя.
- Единственная горутина event-loop'а Instance владеет всей мутацией жизненного цикла (`instance.go:283-369`); track'и отчитываются через `trackEvent`'ы на канале, без блокировки на состоянии жизненного цикла.
- Контракт исполнения узла всё ещё несёт опциональные pre/post-хуки: `exec.NodePrologue` / `exec.NodeEpliogue` (`internal/exec/exec.go:20-36`), вызываемые вокруг `Exec` из `track.go` (`runNodePrologue` `:550-558`, `runNodeEpilogue` `:562-570`). **Только `UserTask` реализует `Prologue`** (`user_task.go:153`, регистрируется на взаимодействие); **`Epilogue` не реализует никто**. Это остаток node-driven-flow-control, который убирает ADR-005 §2.5.

### 1.2 Почему

Parallel split/join — это ядро BPMN и цель M1 roadmap «embedded-library MVP». Пока он не приземлён, движок умеет только линейный flow (плюс Exclusive-ветвление). Он также устанавливает шов синхронизации для остального семейства шлюзов.

## 2. Цели и область

### 2.1 Цели (в области)

- **G1.** Тип узла `ParallelGateway`, который исполняется: расхождение активирует **все** исходящие flow; шлюз реализует `exec.NodeExecutor`.
- **G2.** **Интерфейс синхронизирующего join'а** в `internal/exec`, реализуемый `ParallelGateway`, отличающий синхронизирующие join'ы от pass-through-слияний Exclusive/активности.
- **G3.** **Синхронизирующий join, принадлежащий узлу** (ADR-005 §2.4): `ParallelGateway` держит своё состояние прибытий на каждый instance + **мьютекс на узле**; track вызывает `Arrive` (атомарно) и действует по ответу — незавершающее прибытие входит в новое состояние **`AwaitingMerge`** и его горутина возвращается (track удерживается как запись, эмитится `evAwaiting`); завершающее прибытие сначала завершает join — эмитит `evMerged`, чтобы loop перевёл ожидающие track'и в `TrackMerged` — **прежде** чем исполнить узел, затем исполняет и форкает. Линия создания выжившего остаётся нетронутой (схождение фиксируется собственными записями `Consumed` поглощённых track'ов, а не пере-родительствованием — FR-5b). Loop держит только учёт ожидающих/завершившихся (без решения, без канала вердикта).
- **G4.** Продьюсер `TrackMerged`; исправление off-by-one в `trackState.String()`.
- **G5.** Запускаемый `examples/parallel-gateway/`, демонстрирующий split→join end-to-end (exit 0).
- **G6.** Схлопнуть контракт исполнения узла до единственного `Execute` (ADR-005 §2.5): убрать хуки `NodePrologue`/`NodeEpilogue` и свернуть регистрацию взаимодействия `UserTask` в его `Exec`.

### 2.2 Не-цели (явно отложено — ADR-005 §4)

- Inclusive (OR), Complex, Event-Based шлюзы; продьюсер `TokenWithdrawn`.
- Loop'ы, повторно входящие в join / лишние токены на одном входящем flow (область = **ацикличный, single-pass**).
- Детектирование заблокированного join'а (недостижимая входящая ветка) — задокументированная ошибка BPMN-моделирования.
- Поток данных на каждое исполнение (inputs/outputs/scope, маршрутизируемые вне полей узла) — [ADR-010](../design/ADR-010-process-data-model.md) (seed). Это приземление сохраняет существующий путь данных; G3 (узел join исполняет только выживший track) держит сам join чистым от любого затирания на каждое исполнение, пока не приземлится ADR-010. (Межинстансовая гонка на разделяемом узле уже устранена ADR-009.)

## 3. Требования

### 3.1 Функциональные

| # | Требование |
|---|---|
| FR-1 | `gateways.NewParallelGateway(opts...)` строит `ParallelGateway` (встраивает базовый `Gateway`, те же опции, что у Exclusive — `WithDirection`, базовые id/name/doc). Он реализует `exec.NodeExecutor`. |
| FR-2 | `ParallelGateway.Exec(ctx, re)` возвращает **все** flow `Outgoing()` дословно — без вычисления условий, без default-flow, никогда не ошибается (spec §13.4.1). Управляет расхождением (1→N) и продолжением join'а (исходящие выжившего) идентично. |
| FR-3 | Интерфейс `exec.SynchronizingJoin` (встраивает `NodeExecutor`) с **атомарным** `Arrive(incomingFlowID, arrivingTrackID string) (complete bool, merged []string)`, защищённым собственным мьютексом узла. `ParallelGateway` его реализует: он записывает `arrived[incomingFlowID] = arrivingTrackID` и возвращает `complete ⇔ len(arrived) == len(Incoming())`; при завершении `merged` = id всех поглощённых track'ов (все предыдущие прибытия; завершающее прибытие — выживший и опускается), и набор очищается. Id — не `*track` — держат контракт в слое модели. Exclusive-шлюзы / активности его **не** реализуют. |
| FR-4 | В run-loop'е track'а, перед исполнением текущего узла, если узел реализует `exec.SynchronizingJoin` **и** `len(Incoming()) > 1`, track вызывает `node.Arrive(incomingFlowID, t.ID())`. **Не complete →** track входит в `AwaitingMerge`, эмитит `evAwaiting`, и его горутина **возвращается** (узел она **не** исполняет). **Complete →** track исполняет узел и продолжает (FR-5). |
| FR-5 | На завершающем прибытии выживший track **сначала объявляет слияние** — `Arrive` возвращает id поглощённых track'ов, и выживший эмитит один `evMerged{ track, mergedIDs }` **прежде** чем исполнить узел (ADR-005 §2.5). Loop разрешает эти id по своей собственной карте `tracks` (он — единственный писатель состояния слитых track'ов) и переводит каждый в `TrackMerged` (токен `Consumed`); ожидающие горутины уже вернулись. Выживший **затем** исполняет узел join и продолжает/форкает через `checkFlows`. Loop применяет `evAwaiting`/`evMerged` только к своему реестру — он **не** принимает решения о синхронизации. |
| FR-5b | Слияние **не** изменяет линию выжившего: у токена на join'е много родителей, но `TokenPath.ParentID` держит одного, поэтому поглощённые id **не** сворачиваются в `prev` выжившего (линия создания). Схождение представлено тем, что собственная запись пути каждого поглощённого track'а оканчивается на join'е с `Consumed`. Следовательно ParentID'ы из `Instance.TokenHistory()` образуют ацикличное дерево создания — ни один track не является собственным предком. (`prev` — это `[]string` id track'ов; loop владеет разрешением id→track.) |
| FR-5a | `ParallelGateway` реализует `Clone() flow.Node` (ADR-009 / SRD-006): конфиг разделён по ссылке, набор `arrived` + мьютекс свежие, flow пустые. Граф узлов на каждый instance гарантирует один набор прибытий на instance. |
| FR-6 | Новое промежуточное состояние track'а **`TrackAwaitingMerge`** (проекция токена: `Alive` — токен всё ещё сидит на join'е); слитый track оканчивается в `TrackMerged` (токен `Consumed`, уже отображённый `token.go:86-90`). `trackState.String()` исправлен, чтобы согласоваться с enum (включая новое состояние). |
| FR-7 | Несинхронизирующее слияние (Exclusive / активность, N>1 входящих) сохраняет сегодняшний pass-through (каждое прибытие продолжается независимо) — без изменений. |
| FR-8 | `examples/parallel-gateway/` (новый модуль): Start → Parallel split → два ServiceTask → Parallel join → End, отрабатывает до завершения, exit 0. |
| FR-9 | Убрать `exec.NodePrologue` и `exec.NodeEpliogue` и вызовы `runNodePrologue`/`runNodeEpilogue` из `track.go`; свернуть регистрацию `UserTask.Prologue` в `UserTask.Exec` (зарегистрировать, затем дождаться исхода); обновить `user_task_test.go`. (ADR-005 §2.5.) |

### 3.2 Нефункциональные

| # | Требование |
|---|---|
| NFR-1 | Без гонок: `make ci` `-race` зелёный. Конкурентные прибытия на join сериализуются **мьютексом на узле** (запись → проверка → сбор при срабатывании — одна атомарная критическая секция); горутины ожидающих track'ов вернулись, поэтому ни одна горутина не остаётся работать. |
| NFR-2 | Узел join исполняет/загружает данные только выживший track (ADR-005 §2.4 «прокатиться на прибывшем track») — поэтому исполнение узла join никогда не запускается двумя track'ами одновременно, независимо от (устранённой ADR-009) межинстансовой гонки. |
| NFR-3 | Diff-coverage ≥95 % (цель 100 %) на затронутых строках (covercheck). |
| NFR-4 | Никакого изменения путей исполнения не-шлюзов; поведение Exclusive не изменено. |

## 4. Дизайн и план реализации

### 4.1 Формы (иллюстративно; точные пути `pkg/` — по ADR-003)

```go
// pkg/model/gateways/parallel.go — per-instance node state (ADR-009 v.1), guarded
// by the node's own mutex so concurrent track arrivals are atomic (ADR-005 §2.4).
type ParallelGateway struct {
    Gateway
    // each incoming flow id seen this round -> the id of the track that arrived
    // on it; the single source of truth for the count and the merge set. Fresh
    // on Clone. (mu last for fieldalignment.)
    arrived map[string]string
    mu      sync.Mutex
}

func NewParallelGateway(opts ...options.Option) (*ParallelGateway, error) { /* mirror NewExclusiveGateway */ }

// Clone gives the instance a fresh arrival set + mutex (ADR-009 / SRD-006):
// config shared by reference, state fresh, flows empty.
func (pg *ParallelGateway) Clone() flow.Node { /* Gateway.clone() + fresh arrived + mu */ }

func (pg *ParallelGateway) Exec(ctx context.Context, re renv.RuntimeEnvironment) ([]*flow.SequenceFlow, error) {
    return pg.Outgoing(), nil // all outgoing, unconditional
}

// Arrive records that arrivingTrackID reached the join on incomingFlowID and
// reports completion — atomic; safe for concurrent track callers. On the
// completing arrival it returns the ids of the absorbed tracks (every prior
// arrival; the completing one is the survivor) and clears the set.
func (pg *ParallelGateway) Arrive(incomingFlowID, arrivingTrackID string) (complete bool, merged []string) {
    pg.mu.Lock()
    defer pg.mu.Unlock()
    pg.arrived[incomingFlowID] = arrivingTrackID
    if len(pg.arrived) < len(pg.Incoming()) {
        return false, nil
    }
    for _, id := range pg.arrived {
        if id != arrivingTrackID {
            merged = append(merged, id)
        }
    }
    clear(pg.arrived)
    return true, merged
}

var _ exec.SynchronizingJoin = (*ParallelGateway)(nil)
```

```go
// internal/exec/exec.go — the synchronizing-join seam: the node owns its arrival
// state + serialization; the track calls Arrive directly (no loop round-trip).
// Ids (not *track) keep the contract in the model layer — the node never
// references the runtime track type.
type SynchronizingJoin interface {
    NodeExecutor
    Arrive(incomingFlowID, arrivingTrackID string) (complete bool, merged []string) // atomic
}
```

```go
// internal/instance — two new lifecycle events (notifications, no reply):
//   evAwaiting{track}              — a track reached a join, goroutine returned (AwaitingMerge)
//   evMerged{track, mergedIDs []string} — survivor declares the merge; the loop
//                                   resolves the ids against its own tracks map
//                                   and flips each to Merged (-> Consumed).
// trackEvent gains: mergedIDs []string. stepInfo gains: inFlow *flow.SequenceFlow.
// New track state TrackAwaitingMerge (token projection -> Alive).
// The join node owns the arrival set (flow id -> arriving track id); the loop is
// the sole writer of merged tracks' state. The survivor's prev (creation lineage)
// is NOT folded with the absorbed ids — a token at a join has many parents but
// TokenPath.ParentID holds one; convergence is carried by the absorbed tracks'
// own Consumed path entries (see FR-5b).
```

Track записывает входящий flow, который он прошёл к join'у (`stepInfo.inFlow`, выставляется в `checkFlows`), и передаёт свой собственный id в `Arrive`. *Состояние* прибытий (flow id → track id) живёт на узле join на каждый instance (ADR-009 v.1), сериализуемое под собственным мьютексом узла; id поглощённых track'ов прокатываются обратно к выжившему в возврате `Arrive` и к loop'у в `evMerged`.

### 4.2 Вехи (каждая независимо собираема + CI-зелёная)

- **M1 — Контракт исполнения узла.** Убрать `NodePrologue`/`NodeEpilogue` (интерфейсы + вызовы `runNodePrologue`/`runNodeEpilogue` в `track.go`); свернуть регистрацию `UserTask` в `Exec` (зарегистрировать, затем дождаться); обновить `user_task_test.go`. Упрощает путь executeNode, который правит M3. (ADR-005 §2.5.)
- **M2 — Parallel split.** Тип `ParallelGateway` + `Exec` (все исходящие) + конструктор/опции + юнит-тесты; диспетчеризация через существующий `NodeExecutor`. Демонстрируемо: расхождение в независимые ветки, каждая из которых достигает собственного End.
- **M3 — Синхронизирующий join.** Интерфейс `exec.SynchronizingJoin` (атомарный `Arrive`, принадлежащий узлу, возвращающий id поглощённых + мьютекс на узле); набор прибытий `ParallelGateway` (`flow id → track id`) + `Clone` (FR-5a); проводка `stepInfo.inFlow`; новое состояние `TrackAwaitingMerge` + события `evAwaiting`/`evMerged`; `evMerged` несёт `mergedIDs []string`, которые loop разрешает, чтобы перевести каждый поглощённый track в `TrackMerged`; `prev []string` (линия создания, не свёрнутая с поглощёнными id — FR-5b); исправление `trackState.String()`; юнит/интеграционные тесты (join срабатывает, когда прибыли все; не-выжившие входят в `TrackAwaitingMerge` (горутина возвращается), затем `TrackMerged`→`Consumed`; выживший продолжается; линия остаётся ацикличной; смешанный N→M; `-race`).
- **M4 — Пример + приёмка.** `examples/parallel-gateway/`; запустить e2e (step-13a smoke); `make ci` зелёный; заполнить §7; flip SRD-005 и ADR-005 → Accepted.

## 5. Верификация (Definition of Done)

| # | Проверка | Ожидание |
|---|---|---|
| V1 | Юнит: `ParallelGateway.Exec` возвращает все исходящие flow, никогда не ошибается (FR-2). | возвращены все `Outgoing()`. |
| V2 | Юнит: `Arrive` возвращает `complete=true` только на последнем уникальном входящем flow, атомарен под мьютексом узла и очищает набор при срабатывании (FR-3). | false при частичном, true при полном. |
| V3 | Интеграция: процесс split→join завершается; join срабатывает ровно один раз после прибытия всех веток; не-выжившие входят в `TrackAwaitingMerge` (горутина возвращается), затем становятся `TrackMerged` (токен `Consumed`); выживший track продолжается (FR-4/5/6). | join синхронизируется; одно продолжение. |
| V3a | Интеграция: через join ParentID'ы из `Instance.TokenHistory()` образуют ацикличное дерево создания — ни один track не является собственным предком; поглощённый track сохраняет своего родителя создания и оканчивается `Consumed` (FR-5b). | линия ациклична. |
| V4 | Интеграция: несинхронизирующее слияние (Exclusive/активность, N>1 входящих) по-прежнему пропускает каждое прибытие независимо (FR-7). | поведение без изменений. |
| V5 | Смешанный шлюз (N входящих, M исходящих): выживший исполняет и форкает на M (FR-2 + fork). | M веток продолжаются. |
| V6 | `examples/parallel-gateway/` отрабатывает до завершения, exit 0 (FR-8; step-13a smoke). | exit 0; ожидаемый вывод. |
| V7 | `make ci` зелёный — `-race`-тесты, diff-coverage ≥95 % на затронутых строках, govulncheck; Exclusive и существующие примеры не затронуты (NFR-1/3/4). | всё проходит. |
| V8 | После M1: не осталось интерфейсов `NodePrologue`/`NodeEpilogue` или вызовов хуков в `track.go`; `UserTask` по-прежнему регистрируется, затем ожидает через `Exec`; тесты `user_task` зелёные (FR-9). | хуки ушли, поведение сохранено. |

## 6. Риски и регрессии

- **Join с единственным исполнителем (ADR-005 §2.4 / NFR-2).** Узел join исполняет только выживший (завершающий) track; незавершающее прибытие обязано вызвать `Arrive`, получить `complete=false`, войти в `AwaitingMerge` и дать своей горутине вернуться **без** исполнения узла. Тест утверждает, что не-выжившие никогда его не вызывают. (Мьютекс на узле сериализует конкурентные прибытия; межинстансовая гонка на разделяемом узле уже устранена ADR-009.)
- **Заблокированный join** (недостижимый входящий) подвешивает instance — задокументированная ошибка моделирования, не детектируется (ADR-005 §4).
- **Исправление `trackState.String()`** должно сохранить существующие имена состояний стабильными (только убрать фантом + перевыровнять), чтобы логи/тесты, читающие имена, не сломались.
- **Loop'ы** вне области; join, повторно входимый циклом, здесь не определён (ADR-005 §4).
- **Сворачивание `UserTask.Prologue` в `Exec` (FR-9)** должно сохранить порядок register-then-await (зарегистрироваться на взаимодействие *прежде* ожидания исхода), чтобы человеческое взаимодействие по-прежнему работало; тест `user_task` обязан это утверждать.

## 7. Итог реализации

Приземлено на `feat/parallel-gateway` в пяти commit'ах (doc + четыре вехи; один
предварительный fix движка, всплывший на M4).

**Commit'ы вех**

| Commit | Веха | Что приземлилось |
|---|---|---|
| `4389f29` | Docs | ADR-005 + этот SRD. |
| `0fa2a30` | M1 — контракт исполнения узла | Убраны `exec.NodePrologue`/`NodeEpilogue` и вызовы хуков в `track.go`; регистрация `UserTask` свёрнута в `Exec` (register-then-await). (FR-9 / V8) |
| `2201aa9` | M2 — split | `ParallelGateway` + `Exec` (все исходящие) + конструктор/опции; исправление диспетчеризации `Node()` (также применено к `ExclusiveGateway`). (FR-1/2 / V1) |
| `e47fdca` | M3 — синхронизирующий join | `exec.SynchronizingJoin.Arrive(incomingFlowID, arrivingTrackID) (complete, merged []string)`; на каждый instance `arrived map[string]string` + мьютекс + `Clone`; `track.synchronize`; `TrackAwaitingMerge` + `evAwaiting`/`evMerged`; loop `applyMerged`; `stepInfo.inFlow`; `prev []string` (линия создания, **не** свёрнутая с поглощёнными id — FR-5b); исправление off-by-one в `trackState.String()`. (FR-3/4/5/5a/5b/6 / V2/V3/V3a/V5) |
| `e5748f2` | M4 — пример | `examples/parallel-gateway/` (Start → split → 2 ServiceTask → join → End), отрабатывает до завершения, exit 0. (FR-8 / V6) |

**Предварительный fix** (`3e10385`): `Thresher.launchInstance` отменял контекст
instance через `defer` сразу после неблокирующего `Instance.Run`, убивая каждый
обычный процесс прежде, чем тот исполнится. Всплыл на примере M4; исправлен (cancel
удержан для teardown, не deferred) с регрессионным тестом
(`TestStartProcess_RunsToCompletion`). Вне области SRD-005, но блокировал V6.

**Ключевые файлы**: `internal/exec/exec.go` (интерфейс); `pkg/model/gateways/parallel.go` (узел + Arrive + Clone); `pkg/model/gateways/exclusive.go` (исправление Node()); `internal/instance/track.go` (synchronize, prev, String); `internal/instance/instance.go` (spawnForks, applyMerged, обёртка spawn); `internal/instance/event.go` (evAwaiting/evMerged, mergedIDs); `internal/instance/token.go` (AwaitingMerge → Alive).

**Результаты верификации**

| Проверка | Результат |
|---|---|
| V1 `TestParallelGatewayExec` | 🟢 |
| V2 `TestParallelGatewayArrive` / `…Concurrent` (-race) | 🟢 |
| V3 `TestParallelJoinSynchronizes` | 🟢 |
| V3a `TestParallelJoinLineageAcyclic` | 🟢 |
| V4 pass-through несинхронизирующего слияния (`synchronize` путь `!ok`/≤1-входящий, упражняемый каждым не-join flow) | 🟢 |
| V5 `TestParallelJoinMixed` | 🟢 |
| V6 `examples/parallel-gateway` `go run .` → оба worker'а, "parallel-demo completed", exit 0 | 🟢 |
| V7 `make ci` — `-race`, diff-coverage **100 %** затронутых строк (гейт ≥95 %), govulncheck | 🟢 |
| V8 `TestNewUserTask` / `TestUserTaskExecErrors`; нет остатка prologue/epilogue | 🟢 |

Уточнения дизайна, сделанные при приземлении (относительно набросков §3/§4 в
авторской редакции), сверены обратно в этот SRD и ADR-005: (a) `Arrive` **id-based**
(id track'ов, `[]string`), так что шлюз слоя модели никогда не ссылается на runtime-тип
track — никакого непрозрачного `any`; (b) слияние **не** сворачивает поглощённые id в
`prev` выжившего (FR-5b) — это порождало цикличный `TokenPath.ParentID`.

## 8. Ссылки

- [ADR-005 v.1 Gateways & Joins](../design/ADR-005-gateways-and-joins.md) — концепция, которую это приземляет.
- [ADR-001 v.5 Execution Model](../design/ADR-001-execution-model.md) — §4.4 fork, §4.5 join, §4.7 владение состоянием рантайма.
- [ADR-009 v.1 Per-instance node graph](../design/ADR-009-per-instance-node-graph.md) — узел на каждый instance, на котором живёт набор прибытий join'а; предоставляет `Clone()` узла (FR-5a).
- [bpmn-spec/semantics/gateways.md](../bpmn-spec/semantics/gateways.md) (§13.4.1), [token-flow.md](../bpmn-spec/semantics/token-flow.md).

## 9. Открытые вопросы

- Ничего блокирующего. (Перевзвод loop/excess-token и семантика OR-join отложены в следующую ревизию шлюзов по ADR-005 §4.)

## История документа

| Версия | Дата | Автор | Изменение |
|---|---|---|---|
| v.1 | 2026-06-09 | Руслан Габитов | Draft. Приземляет Parallel (AND) шлюз по ADR-005 v.1: split (все исходящие) + **синхронизирующий join, принадлежащий узлу** — `ParallelGateway` держит свой набор прибытий на каждый instance (ADR-009 v.1) + мьютекс на узле; track вызывает атомарный `Arrive`; незавершающее прибытие входит в новое состояние `TrackAwaitingMerge` и его горутина возвращается (track удержан как запись, `evAwaiting`), завершающее прибытие сначала завершает join (эмитит `evMerged`; loop переводит ожидающие track'и → `TrackMerged`) **прежде** чем исполнить узел, затем исполняет и форкает, оставляя линию создания нетронутой (FR-5b); исправление `trackState.String()`. Также приземляет упрощение контракта исполнения узла из ADR-005 §2.5 (убрать `NodePrologue`/`NodeEpilogue`, свернуть регистрацию `UserTask` в `Exec`) как M1. Четыре вехи (контракт → split → join → пример+приёмка). Область ацикличная/single-pass; OR/Complex/Event-Based и loop'ы/лишние токены отложены. (Сверено при возобновлении с приземлённым ADR-009/SRD-006 + ADR-001 v.5, затем с обсуждением дизайна: синхронизация полностью перенесена на узел — без удерживаемой loop'ом карты, без канала вердикта, без раскола механизм/политика; grounding §1.1 помечен на переверификацию на M1.) |
| v.1 | 2026-06-11 | Руслан Габитов | **Принято.** Приземлено через M1–M4 (`0fa2a30` / `2201aa9` / `e47fdca` / `e5748f2`) + предварительный fix движка (`3e10385`, преждевременная отмена контекста в `Thresher.launchInstance`). Два уточнения при приземлении, сверены в §1.1/§3/§7/§FR и ADR-005 v.1: (a) `Arrive` id-based (`[]string` id track'ов), так что шлюзу слоя модели не нужен `*track`/`any`; (b) слияние **не** сворачивает поглощённые id в `prev` выжившего (новые FR-5b/V3a — сворачивание порождало цикличный `TokenPath.ParentID`). `make ci` зелёный; diff-coverage 100 % на затронутых строках; заметка о переверификации §1.1 закрыта. |
