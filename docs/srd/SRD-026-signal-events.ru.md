# SRD-026 — Signal-события (throw / catch / broadcast + инстанцирование через signal-start)

| Поле | Значение |
|---|---|
| Статус | Принято |
| Версия | v.1 |
| Дата | 2026-06-21 |
| Владелец | Ruslan Gabitov |
| Реализует | [ADR-006 v.1 События и подписки](../design/ADR-006-events-and-subscriptions.ru.md) §2.1/§2.3/§2.4 |

Этот SRD приземляет **signal-события** как специфицированную, сверенную со стандартом
функциональность. Бо́льшая часть механики уже существует (она появлялась инкрементально —
через инфраструктуру событий и работу над Event-Based-шлюзом); этот SRD **специфицирует и
тестирует** приземлённое поведение throw / catch / broadcast и **закрывает единственный
реальный пробел — инстанцирование через signal-*start*** (broadcast-сигнал сейчас не может
инстанцировать процесс, чей стартовый триггер — сигнал).

**Сигнал** — это *broadcast-публикация, unscoped в пределах досягаемости, без корреляции*:
каждый ловящий handler в пределах досягаемости получает его (BPMN §10.5.7 /
`docs/bpmn-spec/semantics/event-handling.md:221`).

---

## 1. Основание

Поддержка сигналов приземлялась по частям и **в значительной мере реализована** (survey
2026-06-21):

- **Определение сигнала** — `SignalEventDefinition` держит name-based `*Signal`
  (`pkg/model/events/signal.go:59`; `Signal.Name()` на `:49`). Имя — ключ broadcast-сопоставления
  (сигналы не несут корреляции).
- **Throw** — триггер сигнала разрешён на intermediate-throw и end-событиях
  (`flow.TriggerSignal` в `intermediate_throw.go:24`, `end.go:25`); оба эмитят через
  `PropagateEvent` хаба.
- **Catch** — разрешён на intermediate-catch (`intermediate_catch.go:21`); ловец регистрирует
  `SignalWaiter` по прибытии track'а (`internal/eventproc/eventhub/waiters/signal.go`).
- **Broadcast** — `EventHub.broadcastSignal` (`eventhub.go:465`) рассыпает брошенный сигнал
  **каждому** waiter'у, чьё определение совпадает **по имени** (`:477`, через `signalName` на
  `:505`); throw без ловца — логируемый **no-op, не ошибка** (`PropagateEvent` возвращает
  `nil` после debug-лога, `eventhub.go:428-437`).
- **Signal-arm'ы Event-Based-шлюза** — `defMatches` сопоставляет signal-arm'ы по имени
  (`pkg/model/gateways/event_based.go:287`), тем же ключом, что и хаб (SRD-024 v.1 §4.3).
- **Пример + тесты** — `examples/signal-broadcast/` (один throw → два watcher-экземпляра
  ловят); `waiters/signal_test.go`, `eventhub_signal_test.go`.

**Два пункта memory теперь устарели (подтверждено survey, зафиксировано здесь):**
- ADR-006 §2.4 «нет waiter'а ⇒ no-op» **уже реализован** — `PropagateEvent` возвращает
  `nil` (не ошибку) для сигнала без зарегистрированного waiter'а (`eventhub.go:428-437`).
  Прежняя пометка «решено, но EventHub всё ещё ошибается» снята.
- Опасение про «non-message broadcast» (конкурентные экземпляры, делящие один catch-waiter,
  оба срабатывают) к сигналам **не** относится: multi-processor рассылка каждому ловцу — это
  ровно корректная broadcast-семантика (`waiters/signal.go` `AddEventProcessor`), а не баг.

**Пробел.** `scanInstantiatingStarts` (`pkg/thresher/instance_starter.go:105`) строит
instance-starter только для `*events.MessageEventDefinition` (тип-пин на `:26`, приведение
на `:135`). Сигнальный StartEvent распознаётся как стартовый узел (`isInstantiatingStartNode`
возвращает true для любого `StartEventClass`-события, `:183-185`), но его
`SignalEventDefinition` пропускается на `:135`, поэтому **стартер не строится** — broadcast-сигнал
никогда не сможет инстанцировать signal-start-процесс.

---

## 2. Требования

### Функциональные

- **FR-1 — signal throw (специфицируется, уже подключено).** Intermediate-throw или end-событие
  с `SignalEventDefinition` рассылает сигнал через `EventHub.PropagateEvent` → `broadcastSignal`.
  Без изменения поведения; этот SRD специфицирует + тестирует его.
- **FR-2 — signal catch (специфицируется, уже подключено).** Intermediate-catch-событие с
  `SignalEventDefinition` подписывает `SignalWaiter` по прибытии track'а и возобновляется, когда
  рассылается сигнал, совпадающий по имени. Без изменения поведения; специфицируется + тестируется.
- **FR-3 — broadcast + no-op без ловца (специфицируется, уже подключено).** Брошенный сигнал
  достигает **каждого** waiter'а, совпадающего по имени (multi-instance рассылка, best-effort на
  ловца); throw без живого ловца — логируемый no-op, не ошибка (ADR-006 §2.4). Специфицируется +
  тестируется.
- **FR-4 — инстанцирование через signal-start (НОВОЕ — ядро реализации).**
  `scanInstantiatingStarts` распознаёт сигнальный StartEvent (нет входящего потока, триггер
  сигнала) и строит стартер, **зарегистрированный на сигнал**, чтобы broadcast хаба достигал
  его; при срабатывании он вызывает `resolveAndLaunch` **без correlation-ключа**, поэтому
  **каждый broadcast инстанцирует** (один broadcast может инстанцировать **несколько**
  signal-start-процессов — broadcast-семантика, в отличие от point-to-point message-start).
  Переиспользует born-from-event стартер (ADR-015 v.1), signal waiter/broadcast и
  `resolveAndLaunch` (пустой ключ ⇒ всегда инстанцирует, без dedup).
- **FR-5 — глобальный broadcast как досягаемость движка (решение).** gobpm рассылает каждому
  signal-waiter'у в пределах всего движка (без фильтрации по scope/reach). Это осознанный выбор
  движка для single-process-pool-движка и защитимый superset «unscoped within reach»
  (§4.4 / Engine notes). Фильтрация по scope BPMN §10.5.7 **отложена** в sub-process-workstream.

### Нефункциональные

- **NFR-1 — переиспользование, без новой подсистемы.** FR-4 переиспользует `instanceStarter` /
  `resolveAndLaunch` (SRD-015) и существующий signal waiter + `broadcastSignal`; этот SRD добавляет
  только распознавание signal-start в сканере. Никакой новой event/correlation-механики.
- **NFR-2 — без корреляции для сигналов.** Сигналы не несут correlation-ключа (BPMN: сигналы не
  коррелируют, `event-handling.md:221`); путь signal-start использует пустой ключ. Контракт
  message-корреляции (ADR-016 v.1) **намеренно не** на signal-пути.
- **NFR-3 — конкурентность.** Broadcast-рассылка и create-путь стартера прогоняются под `-race`;
  конкурентные broadcast'ы в стартер сериализуются `resolveAndLaunch` (mutex `t.seenKeys`), а
  пустой ключ никогда не делает dedup.
- **NFR-4 — аддитивность.** Message-start-инстанцирование, Event-Based-шлюз и все прочие
  шлюзы/события не затронуты; signal-start-путь чисто аддитивен.
- **NFR-5 — покрытие.** Затронутые файлы завершают ≥95% diff-покрытия (`make ci`), цель 100%.

---

## 3. Модели

### 3.1 Обобщить определение триггера у стартера (`pkg/thresher/instance_starter.go`)

Сегодня `instanceStarter.eDef` типизирован `*events.MessageEventDefinition` (`:26`), и
`scanInstantiatingStarts` приводит каждое кандидатное определение к этому типу (`:135`), молча
пропуская `SignalEventDefinition`. Обобщить поле до интерфейса, чтобы стартер мог держать любой
триггер:

```go
type instanceStarter struct {
	thr       *Thresher
	snapshot  *snapshot.Snapshot
	startNode flow.Node
	eDef      flow.EventDefinition // было *events.MessageEventDefinition — теперь message ИЛИ signal
	corrKey   *bpmncommon.CorrelationKey // nil для сигналов (без корреляции)
	id        string
}
```

`scanInstantiatingStarts` принимает и `*MessageEventDefinition` (как сегодня, с его опциональным
`CorrelationKey`), и `*SignalEventDefinition` (с `corrKey = nil`). `deriveKey` возвращает `""`
всякий раз, когда `corrKey == nil` (это уже его поведение), поэтому signal-start выводит пустой
ключ → `resolveAndLaunch` всегда инстанцирует.

### 3.2 Persistent signal waiter для стартера (`internal/eventproc/eventhub/waiters/waiters.go`)

Стартер регистрируется через `RegisterPersistentEvent` → `CreatePersistentWaiter`, который сегодня
**отвергает non-message-триггеры** (`waiters.go:112`: `eDef.Type() != flow.TriggerMessage` → ошибка).
Расширить его, чтобы он также подкреплял **сигнальный** стартер: для `flow.TriggerSignal` строить
`NewSignalWaiter`. Новый тип waiter'а и one-shot-флаг не нужны — persistence **driven процессором**:
catch-track само-отписывается при возобновлении (one-shot), тогда как стартер никогда не
само-отписывается, поэтому остаётся подписанным и срабатывает на каждый broadcast (persistent).
Обновить message-only-комментарий + ошибку на «message or signal».

### 3.3 Прочих изменений модели нет

Throw/catch/broadcast уже существуют; FR-1…FR-3 не добавляют форм. Сам signal waiter, name-индекс
`broadcastSignal` и сопоставление signal-arm'ов Event-Based-шлюза не изменяются.

---

## 4. Анализ

### 4.1 Почему без нового ADR

ADR-006 v.1 §2.1/§2.3/§2.4 **решают** broadcast сигнала, жизненный цикл подписки ловца и no-op
без ловца; ADR-015 v.1 поставляет born-from-event instance-starter, который переиспользует FR-4.
Этот SRD подключает случай signal-start и специфицирует приземлённое поведение. Открытого
концептуального решения нет, поэтому ADR (или bump ADR) не нужен.

### 4.2 Инстанцирование через signal-start (FR-4)

Механика instance-starter (ADR-015) уже: сканирует снапшот на инстанцирующие стартовые узлы,
строит persistent `instanceStarter` на триггер, регистрирует его на EventHub как `EventProcessor`
и при сработавшем событии вызывает `resolveAndLaunch` (пустой ключ ⇒ create; keyed ⇒ dedup).
Единственное, что блокирует сигналы, — message-only приведение на `:135`.

Исправление: в `scanInstantiatingStarts`, когда определение стартового узла —
`*SignalEventDefinition`, строить стартер с `eDef = это signal-определение`, `corrKey = nil`.
Thresher регистрирует его тем же путём `RegisterPersistentEvent` — который §3.2 расширяет так,
что `CreatePersistentWaiter` подкрепляет **persistent signal waiter** для сигнального триггера
(раньше был message-only). Поскольку хаб ключует доставку сигнала по **имени** (`broadcastSignal`
→ `signalName`), broadcast этого сигнала достигает `ProcessEvent` стартера, который вызывает
`resolveAndLaunch(…, key="")` → новый экземпляр **рождённый из сигнального StartEvent**
(pre-fired, бежит из его исходящего).

**Multi-инстанцирование — намеренное.** Если несколько процессов объявляют сигнальный StartEvent
с одним именем сигнала, каждый регистрирует свой стартер; один `broadcastSignal` рассыпается всем
им (совпадение по имени), инстанцируя один процесс **на каждое** объявление signal-start — broadcast-аналог
message-start (который point-to-point). С пустым ключом `resolveAndLaunch` никогда не делает dedup,
поэтому повторные broadcast'ы продолжают инстанцировать.

### 4.3 Throw / catch / broadcast (FR-1…FR-3) — специфицировать приземлённое поведение

Они уже подключены (см. §1). Этот SRD фиксирует их тестами и ссылками на стандарт: signal throw
рассылает по имени (`broadcastSignal`, `eventhub.go:465`); каждый совпадающий по имени ловец в
пределах досягаемости получает его (multi-processor рассылка); throw без ловца — no-op
(`eventhub.go:428-437`, ADR-006 §2.4). Корреляция не участвует (BPMN, `event-handling.md:221`).

### 4.4 Engine notes — глобальная досягаемость broadcast (FR-5)

BPMN §10.5.7 ограничивает *досягаемость* сигнала scope'ом (ловец видит сигнал, опубликованный в
scope, в котором он участвует). gobpm сейчас рассылает **каждому** signal-waiter'у в движке, без
фильтрации по scope. Для single-process-pool, in-memory движка это защитимый **superset**
«unscoped within reach» (`event-handling.md:221` — сигналы unscoped в пределах своей досягаемости;
с одной досягаемостью все ловцы в ней). Scope-ограниченная досягаемость (актуальна, когда появятся
sub-process'ы / несколько pool'ов) **отложена в sub-process-workstream**; этот SRD документирует
выбор глобальной досягаемости, а не реализует scoping.

### 4.5 Вне scope (явно)

- **Signal boundary-события** — инфраструктуры boundary-событий нет вообще; signal boundary —
  часть сквозного **boundary-events-workstream** (signal / error / escalation / timer / conditional
  boundary вместе), не этот SRD.
- **O(1) индекс signal-name → waiters** — `broadcastSignal` — линейный name-scan (`eventhub.go:477`);
  индексированный lookup — отложенная, неблокирующая оптимизация.
- **Обобщение event-matching** (`SubscriptionKey()`) — унификация name-scan хаба + `defMatches`
  шлюза за одним полиморфным ключом отложена до приземления Link-событий (когда второй name-keyed
  тип событий сделает абстракцию окупаемой).

---

## 5. Сценарии тестов (§6)

| # | Тест | Сценарий | Проверяет |
|---|---|---|---|
| 1 | `TestSignalStartInstantiates` | процесс с сигнальным StartEvent (нет входящего); broadcast его сигнала | один новый экземпляр рождён из signal-start и бежит до завершения |
| 2 | `TestSignalStartBroadcastInstantiatesAll` | два процесса, каждый — сигнальный StartEvent на **одно** имя сигнала; один broadcast | **оба** инстанцируются (по одному экземпляру — broadcast, не point-to-point) |
| 3 | `TestSignalStartEachBroadcastNewInstance` | broadcast того же сигнала дважды | два независимых экземпляра (пустой ключ ⇒ без dedup) |
| 4 | `TestSignalCatchThrow` (регрессия, существует) | intermediate signal throw в одном экземпляре, intermediate signal catch в другом | ловец возобновляется на broadcast |
| 5 | `TestBroadcastSignalFanOut` / `TestSignalWaiterBroadcastFanOut` (регрессия, существуют) | один throw, N ждущих ловцов (разные eDef id, одно имя) | все N получают (`eventhub_signal_test.go`, `waiters/signal_test.go`) |
| 6 | `TestSignalThrownIntoVoid` (регрессия, существует) | broadcast сигнала без зарегистрированного ловца | без ошибки; логируемый no-op (`eventhub.go:428-437`) |

`TestSignalBroadcast` (cross-instance broadcast) и `TestSignalSingleShotConsume` (catch потребляет
однократно) в `pkg/thresher/signal_test.go` добавляют покрытия; signal-start стартер покрыт тестами
1–3 там, а `waiters/signal_test.go` + `eventhub_signal_test.go` покрывают waiter + рассылку.

---

## 8. Cross-doc

- **Реализует** [ADR-006 v.1](../design/ADR-006-events-and-subscriptions.ru.md) §2.1/§2.3/§2.4 — broadcast сигнала, жизненный цикл ловца, no-op без ловца (вверх).
- [ADR-015 v.1](../design/ADR-015-event-triggered-instantiation.ru.md) — born-from-event instance-starter, который переиспользует FR-4 (вверх).
- [ADR-005 v.4](../design/ADR-005-gateways-and-joins.ru.md) §2.12 — сопоставление signal-arm'ов Event-Based-шлюза по имени (вверх).
- [SRD-024 v.1](SRD-024-event-based-gateway.ru.md) — прецедент signal-by-name `defMatches` (вбок).
- [SRD-025 v.1](SRD-025-event-based-gateway-instantiation.ru.md) — прецедент расширения `scanInstantiatingStarts`, который этот SRD зеркалит для signal-start (вбок).

Сигналы **не** несут корреляции, поэтому у этого SRD нет зависимости от ADR-016 (корреляция
намеренно отсутствует на signal-пути — NFR-2). Версии запинены; downward-ссылок нет.

## 9. Definition of Done

- FR-1…FR-5 подключены (FR-1…FR-3 уже подключены + теперь специфицированы/протестированы; FR-4 новый); тесты §5 проходят под `-race`.
- `make ci` зелёный: lint, build, `-race`, diff-покрытие ≥95% (цель 100%), govulncheck.
- `examples/signal-broadcast/` smoke exit 0; добавлен signal-**start** пример (процесс, открываемый broadcast-сигналом), smoke exit 0.
- Устаревший пункт memory ADR-006 §2.4 закрыт (no-op уже реализован — §1).
- **Вне scope:** signal boundary-события (boundary-events-workstream); фильтрация scope/reach (sub-process-workstream); O(1) name-индекс + обобщение `SubscriptionKey()` (отложено).

## 10. Сводка реализации

Приземлено в ветке `feat/signal-events` (от `master`).

### 10.1 Этапы по коммитам

| Веха | Коммит | Объём | Тесты |
|---|---|---|---|
| Doc | `c18ab2d` | SRD-026 (этот документ) | — |
| M1 — инстанцирование через signal-start | `65213ab` | `CreatePersistentWaiter` принимает `TriggerSignal` → `NewSignalWaiter` (`waiters.go`); `instanceStarter.eDef` → `flow.EventDefinition`; `scanInstantiatingStarts` принимает `*SignalEventDefinition`; `deriveKey`/`discovery.go` signal-aware (`triggerName`) | `TestSignalStartInstantiates`, `…BroadcastInstantiatesAll`, `…EachBroadcastNewInstance`, `TestTriggerName`, `TestCreatePersistentWaiter` (signal) |
| M2 — регрессия FR-1…FR-3 | (без коммита) | Поведение уже подключено **и протестировано** — `TestSignalCatchThrow`, `TestSignalBroadcast`, `TestSignalThrownIntoVoid`, `TestBroadcastSignalFanOut` существовали ранее; новых тестов не нужно | (существующие) |
| M3 — пример signal-start | `ce50ffe` | `examples/signal-start/` (один broadcast → два signal-start-экземпляра) | smoke exit 0 |

### 10.2 Дельты против черновика

- **Изменение на уровне waiter'а (черновик v.1 его упустил).** Одной §3.1 было недостаточно:
  `CreatePersistentWaiter` (`waiters.go:112`) отвергал non-message-триггеры, поэтому сигнальный
  стартер строился бы, а затем падал при регистрации. M1 добавил фикс §3.2 — persistent signal
  waiter (без нового типа waiter'а; persistence driven процессором, стартер никогда не
  само-отписывается). SRD §3.2/§4.2 поправлены (Draft) и приземлены вместе с кодом M1.
- **M2 оказалась no-op.** Черновик предполагал, что у signal throw/catch/broadcast нет тестов; на
  деле `TestSignalCatchThrow` / `TestSignalBroadcast` / `TestSignalThrownIntoVoid` /
  `TestBroadcastSignalFanOut` уже существовали и проходят. Имена в §5 выровнены под реальные тесты;
  новых тестов не писалось.
- **Два устаревших пункта memory подтверждены + закрыты:** no-op без ловца ADR-006 §2.4 уже
  реализован (`eventhub.go:428-437`), а опасение про non-message broadcast к сигналам не относится
  (multi-processor рассылка корректна).

### 10.3 Верификация (V-результаты)

- `make ci` зелёный на HEAD: tidy, lint, build, `-race`, **diff-покрытие 97.1%** (`COVER_MIN` 95;
  covercheck v0.1.2 исключает log-строки), govulncheck чисто.
- Все 16 `examples/` smoke зелёные (exit 0), включая новый `examples/signal-start`.
- Тесты §5 проходят под `-race`; message-start-инстанцирование не затронуто (его тесты проходят).

## Открытые вопросы

- **Нет.**
