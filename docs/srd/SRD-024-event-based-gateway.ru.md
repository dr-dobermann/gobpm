# SRD-024 — Event-Based gateway (Exclusive отложенный выбор в середине потока)

| Поле | Значение |
|---|---|
| Статус | Принято |
| Версия | v.1 |
| Дата | 2026-06-20 |
| Владелец | Руслан Габитов |
| Реализует | [ADR-005 v.4 Gateways & Joins](../design/ADR-005-gateways-and-joins.ru.md) §2.12 |

Этот SRD приземляет **Exclusive Event-Based gateway в середине потока**, решённый в
[ADR-005 v.4](../design/ADR-005-gateways-and-joins.ru.md) §2.12: **отложенный выбор**
(WCP-16), реализованный как **gate-as-router** — один track шлюза владеет всеми
подписками своих arm'ов и при первом событии маршрутизирует его в выигравший arm и
продвигает токен на путь этого arm'а; остальные подписки сбрасываются (никаких токенов
на arm'ах, поэтому никакого withdrawal). Arm'ы = Message/Timer/Signal catch events +
Receive Tasks.

**Parallel вне scope** — по спецификации это конструкция инстанцирования (только
start-only, нет Parallel в середине потока; ADR-005 v.4 §2.12.3), поэтому он приземляется
с **follow-up SRD по instantiator'у**; его семантика — это **completion gate**
(проверено §10.6.6/§13.2 — каждый arm продолжается по мере срабатывания своего события,
только completion ждёт всех). Формы **instantiator** и arm'ы **Conditional** также вне
scope (§9).

---

## 1. Background

ADR-005 v.4 §2.12 решает Event-Based gateway; реализации не существует
(`pkg/model/gateways/` имеет Exclusive/Parallel/Inclusive/Complex, нет event gateway).
Шлюз сильно связан с событиями, но нужная ему машинерия уже существует:

- **Catch-event wait / resume.** Track, достигший event node, переходит в
  `TrackWaitForEvent` и регистрирует каждое определение (`internal/instance/track.go`
  `synchronize`/`run` — `RegisterEvent(t, eDef)` на каждое определение); когда событие
  срабатывает, hub вызывает `ProcessEvent` track'а, который делегирует
  `node.ProcessEvent`, чтобы привязать payload, отменяет регистрацию определений узла и
  возвращает track в `TrackReady`, чтобы `run()` возобновился (`track.go`
  `ProcessEvent`). Event-Based gateway переиспользует это **целиком** — он лишь меняет
  *кто* владеет подписками (шлюз, для всех своих arm'ов) и добавляет шаг *маршрутизации*
  (сработавшее определение принадлежит arm'у, а не собственному узлу шлюза).
- **Реестр подписок.** `internal/eventproc/eventhub` `RegisterEvent` /
  `UnregisterEvent` / `RemoveEventProcessor` — регистрирует `eventproc.EventProcessor`
  (`pkg/eventproc/eventproc.go:18` `ProcessEvent(context.Context, flow.EventDefinition)
  error`) для определения; unregister снимает его; hub — единственный владелец waiter'а
  ([ADR-006 v.1](../design/ADR-006-events-and-subscriptions.ru.md) §2.5).
- **Доступ к arm'ам.** Шлюз достигает своих arm'ов структурно: `flow.Node.Outgoing()`
  (`pkg/model/flow/node.go:75`) → каждый `*SequenceFlow.Target()`
  (`sequenceflow.go:290`) — это arm-узел; arm, являющийся `flow.EventNode`
  (`flow/events.go:82`), выставляет `Definitions() []EventDefinition` (события, на которые
  подписываться) и реализует `eventproc.EventProcessor`, чтобы их привязать.
- **Паттерны gateway + validation.** `gateways.New(opts)` + per-type обёртка
  (`gateways/exclusive.go` `NewExclusiveGateway`); per-node validation при регистрации
  через хук `interface{ Validate() error }` `Process.Validate`
  (`pkg/model/process/process.go:238`, добавлен SRD-023).

**Пробел.** Нет `EventBasedGateway`; нет шлюза, который подписывается на *несколько*
arm'ов и маршрутизирует по first-fire; token-state `TokenWithdrawn`
(`internal/instance/token.go`) — это **mis-model**, подлежащий упразднению (ADR-005 v.4
§2.12.1 — токенов на arm'ах нет).

---

## 2. Requirements

### Functional

- **FR-1 — модельный тип `EventBasedGateway`.** Новый `pkg/model/gateways/event_based.go`
  `EventBasedGateway`, встраивающий `Gateway` (зеркалит `ExclusiveGateway`,
  `exclusive.go:15`), **расходящийся**, **Exclusive** (единственная конфигурация в
  середине потока — ADR-005 v.4 §2.12.3; Parallel — start-only, отложен). `Clone()`
  (свежее per-instance состояние arm'ов, ADR-009), `Node()`.
- **FR-2 — шлюз владеет всеми подписками arm'ов.** Шлюз переиспользует существующий путь
  wait-регистрации (`track.go:330–349`, который гейтит на
  `node.(eventproc.EventProcessor)` и итерирует `Definitions()` узла):
  `EventBasedGateway` реализует `flow.EventNode` с `Definitions()`, возвращающим
  **объединение определений своих arm'ов** (собранных из `Outgoing()[i].Target()` —
  каждый `flow.EventNode` или Receive Task), так что когда токен шлюза приходит, track
  шлюза переходит в `TrackWaitForEvent` и регистрирует их все с **track'ом шлюза** в роли
  `eventproc.EventProcessor` — код регистрации не меняется. Ни на одном arm'е токен не
  производится (ADR-005 v.4 §2.12.1).
- **FR-3 — маршрутизация при срабатывании.** При `ProcessEvent(ctx, eDef)` шлюз
  разрешает `eDef → выигравший arm-узел`, делегирует `ProcessEvent(ctx, eDef)` этого
  arm'а (arm привязывает свой собственный payload — message/item), продвигает **step
  track'а шлюза на arm-узел** и возвращает его в `TrackReady`, чтобы `run()` возобновился
  в arm (уже удовлетворённый — он не перезапрашивает) и дальше на продолжение arm'а.
- **FR-4 — Exclusive policy (первый сработавший выигрывает).** Первый сработавший
  выигрывает: испускает один токен на путь выигравшего arm'а и делает
  `UnregisterEvent` для определений каждого другого arm'а. Решение **принадлежит циклу**
  (цикл сериализует срабатывания; первый обработанный выигрывает) — никакого race'а на
  стороне track'а (NFR-2).
- **FR-5 — никакого withdrawal; упразднение `TokenWithdrawn`.** Проигравшие arm'ы никогда
  не получали токен, поэтому шлюз лишь сбрасывает их подписки; token-state
  `TokenWithdrawn` удаляется (`internal/instance/token.go:25,28` + его
  `String()`/range guard на `:43,:53`) вместе с его проекцией
  (`internal/instance/observer_test.go:23`) (ADR-005 v.4 §2.12.1).
- **FR-6 — валидация (регистрация).** `EventBasedGateway` реализует `Validate() error`
  (per-node хук `Process.Validate`, `process.go:238`), проверяя относительно своих
  теперь-связанных потоков (ADR-005 v.4 §2.12.5): (a) **≥2 исходящих arm'а**; (b) каждый
  arm — это промежуточный **Message/Timer/Signal catch event или Receive Task**;
  (c) каждый arm имеет **ровно один входящий поток** (этот шлюз); (d) **нет
  `conditionExpression`** на исходящих потоках шлюза; (e) **нет boundary events на
  Receive-Task arm'е**; (f) **Message catch event и Receive Task не сосуществуют**
  (FR-7).
- **FR-7 — взаимоисключение Message-catch / Receive-Task.** Шлюз, имеющий **оба** —
  Message intermediate catch event arm и Receive Task arm — отвергается при регистрации:
  оба потребляют messages, поэтому маршрутизация неоднозначна (BPMN §10.6.6: "If
  Message Intermediate Events are used … Receive Tasks MUST NOT be used … and vice
  versa"). Timer/Signal catch arm'ы свободно смешиваются с Receive Task; этот запрет
  защищает реальную неоднозначность, поэтому он принудительный, а не опциональный.
- **FR-8 — per-instance состояние arm'ов.** Учёт armed/fired шлюза (какой arm выиграл,
  чтобы держать срабатывание идемпотентным, а отписку корректной) — **per-node,
  per-instance**, создаётся заново `Clone()` (ADR-009) и мутируется под собственным
  mutex'ом шлюза (NFR-2).

### Non-functional

- **NFR-1 — переиспользовать, не перестраивать.** Никакой новой event-подсистемы:
  регистрация, доставка, привязка payload'а и resume — это существующий путь
  `RegisterEvent`/`UnregisterEvent`/`ProcessEvent`/`TrackWaitForEvent`; шлюз добавляет
  только multi-arm подписку + маршрутизацию.
- **NFR-2 — race, принадлежащий циклу.** Решение fire/withdraw/complete выполняется на
  цикле instance'а (единственный писатель состояния track'а), как у синхронизирующих
  join'ов (ADR-005 §2.4/§2.10/§2.11); goroutine track'а никогда не решает race. Проверено
  под `-race`.
- **NFR-3 — per-instance идентичность подписки.** Point-to-point определения arm'ов
  (Message/Timer) клонируются на каждый instance (`CloneForInstance`), чтобы два
  instance'а, гоняющие один шлюз, не cross-fire'или друг друга; signals остаются
  broadcast (ADR-006 §2.1).
- **NFR-4 — Parallel/OR/Complex не затронуты.** Остальные шлюзы сохраняют свои контракты;
  путь event-gateway аддитивен.
- **NFR-5 — покрытие.** Затронутые файлы финишируют с ≥95% diff-coverage (`make ci`
  `cover-check`), цель 100%.

---

## 3. Models

### 3.1 `EventBasedGateway` (`pkg/model/gateways/event_based.go`)

```go
// EventBasedGateway is a diverging Exclusive deferred choice: it subscribes to all its
// arms' events and routes by which fires first; the other subscriptions are dropped. The
// gate owns the wait — no token ever sits on an arm (ADR-005 v.4 §2.12). Parallel is a
// start-only instantiation construct and is out of this SRD's scope (§2.12.3).
type EventBasedGateway struct {
	Gateway
}
```

В этом срезе шлюз не несёт ни статической policy, ни per-instance состояния arm'ов:
победитель решается runtime'ом по мере срабатывания событий (§4.2), а single-fire guard —
это существующее состояние `TrackWaitForEvent` плюс unregister-all (§10 delta vs FR-8).

### 3.2 Конструктор

```go
// NewEventBasedGateway builds a diverging Exclusive Event-Based gateway (just New(opts);
// no gateway-specific options). Arm well-formedness is checked at registration.
func NewEventBasedGateway(opts ...options.Option) (*EventBasedGateway, error)
```

(`WithDirection(Diverging)` унаследован от `Gateway`; сходящийся Event-Based gateway не
является BPMN-формой. `EventGatewayType`/`WithEventGatewayType` приходят с SRD по
Parallel instantiator'у.)

---

## 4. Analysis

### 4.1 Шлюз как маршрутизирующий `EventProcessor` (разделение model vs runtime)

Сегодня `track.ProcessEvent` (runtime) предполагает, что сработавшее определение
принадлежит **текущему** узлу track'а, и вызывает `node.ProcessEvent` для привязки. Для
шлюза текущий узел — это **gateway**, но сработавшее определение принадлежит одному из его
**arm**-узлов — поэтому работа разделяется на два слоя:

- **Model layer (`EventBasedGateway`).** `Definitions()` возвращает объединение arm'ов
  (FR-3, для регистрации); `ProcessEvent(eDef)` **разрешает владеющий arm** (сканирует
  `Definitions()` arm'ов в `Outgoing()[i].Target()`) и **делегирует `ProcessEvent(eDef)`
  этого arm'а**, чтобы arm привязал свой собственный payload. Модельный узел не имеет
  track'а и не может трогать runtime-состояние.
- **Runtime layer (`track.ProcessEvent`, расширен).** Когда текущий узел —
  `EventBasedGateway`, после того как модель маршрутизирует привязку, он **продвигает step
  track'а на разрешённый arm**, делает `UnregisterEvent` для определений других arm'ов и
  возвращается в `TrackReady` (§4.2).

Разрешение + продвижение step — единственная новая логика; привязка/resume/unregister
переиспользуются.

### 4.2 После срабатывания — первый выигрывает, остальные сбрасываются

При первом срабатывании цикл продвигает единственный токен на выигравший arm (§4.1) и
делает `UnregisterEvent` для определений каждого другого arm'а; per-instance состояние
arm'ов записывает, что шлюз сработал, поэтому sibling-событие, бывшее in-flight в момент
сброса его подписки, становится no-op. Один токен внутрь, один наружу; никаких токенов на
arm'ах, никакого withdrawal (FR-5).

### 4.3 Почему race принадлежит циклу

Срабатывание входит через hub → `ProcessEvent` track'а шлюза. Чтобы держать решение
first-wins и отписку sibling'ов свободными от race'ов goroutine'ы track'а (та же
опасность, что задела OR-join/Complex, ADR-005 §2.4/§2.10/§2.11), *решение* принимается на
цикле instance'а: `ProcessEvent` записывает срабатывание и сигналит циклу, который
выполняет маршрутизацию + (Exclusive) отписку + продвижение step как единственный писатель
состояния track'а.

### 4.4 Размещение валидации

Проверки `count`/структуры познаваемы только после связывания, поэтому они выполняются при
регистрации через per-node хук `Validate()` (`process.go:238`). Шлюз инспектирует свои
arm'ы из `Outgoing()` (их типы узлов, число `Incoming()` каждого arm'а, отсутствие
`conditionExpression`, boundary events Receive-Task'а и взаимоисключение Message-catch /
Receive-Task по §10.6.6).

### 4.5 Упразднение `TokenWithdrawn`

Зарезервированный `TokenWithdrawn` из `internal/instance/token.go` был заглушкой для
producer'а race-loser'а, который, согласно ADR-005 v.4 §2.12.1, не существует (нет токенов
на arm'ах). Он и любая ссылка на него удаляются; race-loser'ы — это чистые сбросы
подписок.

---

## 5. Test scenarios (§6)

| # | Test | Сценарий | Утверждает |
|---|---|---|---|
| 1 | `TestEventGatewayExclusiveFirstWins` | gate → {message arm, timer arm}; сработать message | путь message arm'а исполняется, timer arm сброшен, instance завершается один раз |
| 2 | `TestEventGatewayExclusiveTimerWins` | то же; дать сработать timer'у первым | путь timer'а исполняется, message arm отписан |
| 3 | `TestEventGatewayReceiveTaskArm` | gate → receive-task arm + signal arm | путь receive-task исполняется на своём message |
| 4 | `TestEventGatewayRace` (`-race`) | конкурентные срабатывания на двух arm'ах | ровно один путь исполняется, нет race'а, нет двойного |
| 5 | `TestEventBasedGatewayValidate` | <2 arm'ов / не-arm узел / arm с 2 входящими / arm-flow с условием / boundary на receive-arm / **message-catch + receive-task** | каждое отвергнуто; **timer/signal-catch + receive-task принимается** |
| 6 | model-unit | `NewEventBasedGateway`, `Clone`, `ArmFor` (signal-by-name) | построение + разрешение arm'а |

In-package (`internal/instance`) тесты покрывают маршрутизацию для per-package coverage
(cross-package thresher тесты не считаются — урок SRD-022/023).

---

## 8. Cross-doc

- **Реализует** [ADR-005 v.4](../design/ADR-005-gateways-and-joins.ru.md) §2.12 (вверх).
- [ADR-006 v.1](../design/ADR-006-events-and-subscriptions.ru.md) §2.1/§2.5 — доставка
  подписок + lifecycle единственного hub-waiter'а (вбок/вверх).
- [ADR-009 v.1](../design/ADR-009-per-instance-node-graph.ru.md) — per-instance состояние
  узла через `Clone` (вверх).
- [SRD-023 v.1](SRD-023-complex-gateway.ru.md) — переиспользуемый per-node хук `Validate`
  (вбок).

Никаких ссылок вниз; версии запинены.

---

## 9. Definition of Done

- FR-1…FR-7 подключены (FR-8 снят — §10.4); §5 тесты присутствуют и проходят под `-race`.
- `make ci` зелёный: lint, build, `-race`, diff-coverage ≥95% (цель 100%), govulncheck.
- `examples/` получает пример event-based-gateway (Exclusive отложенный выбор), smoke
  exit 0.
- Standard-claims ADR-005 v.4 проверены против BPMN PDF: правило смешивания (§10.6.6 —
  запрещён только Message-catch + Receive-Task) и **completion-gate** Parallel'а
  (§10.6.6 / §13.2).
- **Вне scope (отложено, ADR-005 v.4 §2.12.7):** конфигурация **Parallel** (start-only —
  семантика completion-gate, проверена) и оба **instantiator'а** (Exclusive-start,
  Parallel-start — born-from-event + correlation), всё в follow-up SRD; arm'ы
  **Conditional** (нужен conditional waiter); перевзведение loop (engine-wide, §4).

## 10. Implementation summary

Приземлён на `feat/event-based-gateway` (от `master`): документ + три milestone'а.

### 10.1 Commits

| M | Commit | Scope |
|---|---|---|
| doc | `1524c13` | SRD-024 (этот документ) |
| M1 | `b8b93ad` | модель `EventBasedGateway` + опции + `Validate` + model-unit тесты |
| M2 | `97030f0` | runtime-маршрутизация (`track.ProcessEvent`→`advanceToArm`); `TokenWithdrawn` упразднён |
| M3 | `adf39c4` | thresher тесты + пример; фикс signal-маршрутизации `defMatches` |

ADR-005 v.4 (§2.12) — это решение; `6af7c0f`.

### 10.2 Key files

- `pkg/model/gateways/event_based.go` — `EventBasedGateway`,
  `Definitions`/`EventClass` (flow.EventNode), `ArmFor`/`defMatches`/`ProcessEvent`
  (eventproc.EventProcessor), `Exec` (фейлит громко), `Validate`.
- `internal/instance/track.go` — интерфейс `eventRouter` + `advanceToArm` и ветвь
  `ProcessEvent` для шлюза.
- `internal/instance/token.go` + `pkg/thresher/handle.go` — `TokenWithdrawn` удалён.
- `examples/event-based-gateway/`.

### 10.3 Verification

- `make ci` зелёный: lint, build, `-race`, diff-coverage **99.6%** (≥95), govulncheck.
- `event_based.go` 100% (model-unit, external + in-package); `advanceToArm` 100%.
- Тесты: model-unit, in-package маршрутизация (`-race`), thresher deferred-choice (signal
  first/second-wins + concurrent) и receive-task arm через broker.
- Все 14 примеров `examples/` smoke exit 0.

### 10.4 Deltas vs the draft

- **Правило смешивания исправлено PDF-проверкой (post-draft переработка).** Драфт и код M1
  отвергали *любую* смесь catch-event + Receive-Task и параметризовали её
  (`WithMixedArms`). BPMN PDF (**§10.6.6**, не §13.4.4) запрещает только сосуществование
  **Message** intermediate event с Receive Task; Timer/Signal + Receive Task разрешено.
  Переработали `Validate` под правило §10.6.6 и **убрали `WithMixedArms` /
  `allowMixed`** (предпосылка о его послаблении была неверным прочтением). Та же проверка
  подтвердила, что Parallel — это **completion gate**, а не barrier (§2.12.3). Это ровно
  тот класс ошибки, для отлова которого существует verification standard-claim'ов.
- **`defMatches` (M3 — реальный фикс).** `ArmFor` сначала сопоставлял сработавшее событие
  по **id**, но broadcast **Signal** доставляется как определение *thrower'а* (EventHub
  маршрутизирует signals по **name**, не по id), поэтому signal arm никогда не разрешался,
  и шлюз парковался навсегда. `defMatches` теперь сопоставляет point-to-point триггеры
  (Message/Timer) по id, а Signals по name. Более ранние milestone'ы срабатывали signals
  только через собственное определение arm'а, а messages — point-to-point, поэтому
  broadcast-путь не упражнялся до thresher signal-теста.
- **FR-8 снят.** Per-instance fired-state не нужен — существующий guard `TrackWaitForEvent`
  в `ProcessEvent` (второе, in-flight событие находит track уже `Ready` → отвергается)
  плюс unregister-all дают single-fire бесплатно.
- **Упразднение `TokenWithdrawn` шире, чем перечислено в §1/FR-5.** Также **публичное**
  значение `thresher.TokenState` + mapping в `handle.go` + оба mapping-теста, не только
  `token.go`/`observer_test.go`.
- **`advanceToArm` возвращает void, а не error.** Его промах `ArmFor` недостижим — шлюзовой
  `ProcessEvent` разрешил+привязал arm прямо перед этим — поэтому error-путь был мёртвым;
  гипотетический промах деградирует безопасно (step не добавлен → цикл перевходит в шлюз →
  `gate.Exec` фейлит громко).
- **Receive-task arm протестирован на уровне thresher (M3), не in-package (M2).** Message
  недоставляем через in-package `PropagateEvent` (только signal-broadcast); ему нужен
  broker, поэтому этот arm покрыт thresher-тестом.

## Открытые вопросы

- **Нет.**
