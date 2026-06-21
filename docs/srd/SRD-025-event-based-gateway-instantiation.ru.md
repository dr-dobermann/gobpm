# SRD-025 — Инстанцирование Event-Based gateway (Exclusive-start + Parallel-start)

| Поле | Значение |
|---|---|
| Статус | Принято |
| Версия | v.1 |
| Дата | 2026-06-21 |
| Владелец | Руслан Габитов |
| Реализует | [ADR-005 v.4 Gateways & Joins](../design/ADR-005-gateways-and-joins.ru.md) §2.12.3/§2.12.4 |

Этот SRD приземляет **инстанцирующий** Event-Based gateway, решённый в
[ADR-005 v.4](../design/ADR-005-gateways-and-joins.ru.md) §2.12.4 — отложенную половину
шлюза (Exclusive-форма в середине потока приземлена в [SRD-024](SRD-024-event-based-gateway.ru.md)).
Шлюз **без входящего потока** и с `instantiate=true` — это **инстанциатор** уровня
определения:

- **Exclusive-start** (`eventGatewayType=Exclusive`, по умолчанию): каждое подходящее
  событие **создаёт новый экземпляр**, исполняемый с этого arm'а; шлюз не ждёт остальные
  события (BPMN §13.2 / §10.6.6).
- **Parallel-start** (`eventGatewayType=Parallel`): **первое** событие создаёт **один**
  экземпляр; **остальные arm'ы** шлюза перевооружаются как in-instance приёмники,
  **скоррелированные с этим экземпляром**; каждый arm продвигается по мере срабатывания
  своего события; экземпляр **завершается только после того, как сработали все arm'ы
  шлюза** (completion-gate из §2.12.3).

Он снимает отложение из [ADR-015 §2.6](../design/ADR-015-event-triggered-instantiation.ru.md).

---

## 1. Предпосылки

[SRD-024](SRD-024-event-based-gateway.ru.md) приземлил шлюз в середине потока
(gate-as-router, побеждает первое сработавшее). **Инстанцирующий** шлюз был отложен
(ADR-015 §2.6: «event-based gateway, используемый на старте … и старт через
parallel-event-gateway»). Нужная ему механика по большей части уже в master (обзор
2026-06-21):

- **Born-from-event.** `pkg/thresher/instance_starter.go` `scanInstantiatingStarts`
  (`:99`) строит persistent-`instanceStarter` уровня определения для каждого
  инстанцирующего стартового триггера (`len(n.Incoming()) == 0 && isInstantiatingStartNode(n)`,
  `:103`); при срабатывании вызывает `Thresher.resolveAndLaunch` (`thresher.go:589`) →
  `launchInstanceFromEvent` → `instance.NewFromEvent` (`instance.go:269`), который засевает
  payload сообщения + ключ беседы и исполняет с исходящего потока стартового узла.
  `isInstantiatingStartNode` (`:146`) сегодня распознаёт только стартовое
  событие-сообщение `StartEvent` и `instantiate=true` `ReceiveTask`.
- **Create-or-route-or-join по ключу.** `resolveAndLaunch` (`thresher.go:589`) — пустой
  ключ ⇒ всегда инстанцировать; непустой ключ ⇒ атомарная дедупликация через
  `t.seenKeys` (второй старт с тем же ключом присоединяется, без дубля). Фаза 2b.
- **Conversation-token threading (фаза 2c, приземлена — SRD-017).** Рождённый экземпляр
  засевает свой ключ беседы (`withConversationKey`/`associateConversationKey`,
  `instance.go:138`/`:329`); in-instance приёмники объявляют `CorrelationKeys()`
  (`track.go`), и membroker маршрутизирует follow-up сообщение в **конкретный**
  keyed-приёмник внутри экземпляра предпочтительнее, чем в стартер уровня определения
  (маршрутизация по специфичности). Доказано `pkg/thresher/conversation_routing_test.go`.
- **Паттерн instantiate.** `activities.ReceiveTask` уже моделирует осведомлённость о
  старте: `WithInstantiate()` (`receive_task_options.go:46`) + `Instantiate() bool`
  (`receive_task.go:119`). Шлюз зеркалит это.
- **Шлюз в середине потока.** `pkg/model/gateways/event_based.go` `EventBasedGateway`
  (SRD-024) — `Definitions()` (объединение arm'ов), `ArmFor`/`defMatches`, маршрутизация
  `ProcessEvent`, `Validate`. Только середина потока; без `instantiate`/`eventGatewayType`.

**Пробелы.** (1) у шлюза нет `instantiate`/`eventGatewayType`; (2)
`isInstantiatingStartNode`/`scanInstantiatingStarts` не знают шлюз (один стартер должен
покрыть его **несколько arm'ов**); (3) **у Parallel-start нет completion-gate** —
`Instance.loop()` завершается на `active == 0` (`instance.go:667`) без понятия «этот
экземпляр должен сначала увидеть срабатывание всех arm'ов шлюза».

---

## 2. Требования

### Функциональные

- **FR-1 — стартовые атрибуты шлюза.** `EventBasedGateway` получает `WithInstantiate()` +
  `Instantiate() bool`, `WithEventGatewayType(EventGatewayType)` +
  `EventGatewayType() EventGatewayType` (enum `{ ExclusiveEvents (по умолчанию),
  ParallelEvents }`, переиспользуемый — только для старта), `WithCorrelationKey(*CorrelationKey)` +
  `CorrelationKey()` (корреляция уровня шлюза, см. FR-2) и удобный
  `ParallelStart() bool` (`instantiate && gwType == ParallelEvents`, читается рантаймом
  структурно). Всё переносится через `Clone()` (ADR-009).
- **FR-2 — валидация старта (при регистрации).** Расширить `Validate` (ADR-005 v.4 §2.12.5):
  **инстанцирующий** шлюз (`Instantiate()`) должен иметь **отсутствие входящего потока** и
  только **message-based** arm'ы (Message catch / Receive Task — BPMN §10.6.6 / §13.2);
  `ParallelEvents` **требует** `Instantiate()` (не-инстанцирующий шлюз ОБЯЗАН быть
  Exclusive, §10.6.6); не-инстанцирующий шлюз сохраняет правила середины потока §2.12.5.
  Parallel-start шлюз объявляет **один** `CorrelationKey` уровня шлюза (`WithCorrelationKey`,
  FR-1), чьё свойство несёт retrieval-выражение на каждое сообщение arm'а — так стартер
  выводит один и тот же ключ беседы из того arm'а, что сработал первым, а остальные
  маршрутизируются в этот экземпляр (BPMN §8.4.2). Ключ живёт на **шлюзе**, не на arm'ах
  (промежуточные catch-события / receive task'и не имеют собственного объявления
  корреляции).
- **FR-3 — распознавание стартера.** `isInstantiatingStartNode` распознаёт
  инстанцирующий `EventBasedGateway`; `scanInstantiatingStarts` строит стартер, который
  покрывает **все** arm'ы шлюза — зарегистрированный (persistent) на определении
  сообщения каждого arm'а, так что инстанцировать может любой arm.
- **FR-4 — Exclusive-start (мульти-альтернативный инстанциатор).** Каждое появление
  события любого arm'а → **новый экземпляр** через `resolveAndLaunch` (рождённый из
  шлюза, маршрутизированный в сработавший arm); экземпляр не ждёт остальные события шлюза
  — «первое подходящее событие» — это *по-экземплярная* точка остановки гонки, не
  one-shot (BPMN §10.5.6: «каждое появление … ведёт к созданию нового экземпляра процесса
  … единственный сценарий, где шлюз может существовать без входящего Sequence Flow»;
  §13.2; §10.6.6 — маркер instantiate — это Multiple Start Event). Без ключа корреляции
  каждое событие создаёт свой экземпляр; с ключом применяется дедупликация `seenKeys`,
  как для любого keyed-старта.
- **FR-5 — рождение Parallel-start.** Первое событие arm'а (ключ `K`) создаёт **один**
  экземпляр (рождённый из шлюза, засеянный `K`); продолжение сработавшего arm'а
  исполняется, а **остальные arm'ы шлюза перевооружаются как in-instance приёмники,
  keyed на `K`** (переиспользуя маршрутизацию по специфичности из фазы 2c), так что
  сообщение последующего arm'а достигает *этого* экземпляра.
- **FR-6 — completion-gate Parallel-start (автоматический).** Parallel-start экземпляр
  завершается только когда сработал **каждый** arm (§2.12.3 — экземпляр «завершается
  только если произошли все события …», §13.2). Это достигается **без выделенного поля
  gate**: born-path засевает ещё не сработавшие arm'ы шлюза как **ожидающие track'и**,
  которые держат счётчик `active` экземпляра `> 0` до прихода их событий, так что
  существующее завершение по `active == 0` (`instance.go:667`) уже блокируется на всех
  arm'ах. Arm продвигается по мере прихода своего события (без барьера); несработавшие
  arm'ы блокируют только *завершение*.
- **FR-7 — Exclusive по умолчанию / без instantiate остаётся серединой потока.** Без
  `WithInstantiate` шлюз — это в точности Exclusive-шлюз середины потока из SRD-024 (без
  изменения поведения); `ParallelEvents` без `Instantiate` — ошибка сборки/регистрации
  (FR-2).

### Нефункциональные

- **NFR-1 — переиспользование.** Born-from-event (`instanceStarter`/`NewFromEvent`/`resolveAndLaunch`)
  и keyed-маршрутизация фазы 2c переиспользуются; этот SRD добавляет распознавание шлюза,
  мульти-arm стартер и completion-gate — без новой подсистемы событий/корреляции.
- **NFR-2 — владение циклом.** Completion-gate вычисляется в цикле экземпляра
  (единственный писатель состояния экземпляра), согласованно с механикой join/gate
  (ADR-005 §2.4/§2.10/§2.11/§2.12).
- **NFR-3 — конкурентность.** Create-or-route стартера атомарен (`t.seenKeys`,
  `thresher.go`); arm'ы, срабатывающие конкурентно в Parallel-экземпляр, сериализуются
  циклом. Проверено под `-race`.
- **NFR-4 — середина потока / другие шлюзы не затронуты.** Поведение SRD-024 в середине
  потока и остальные шлюзы не изменены; стартовый путь аддитивен.
- **NFR-5 — покрытие.** Затронутые файлы завершают с ≥95% diff-покрытия (`make ci`),
  цель 100%.

---

## 3. Модели

### 3.1 Стартовые атрибуты `EventBasedGateway` (`pkg/model/gateways/event_based.go`)

```go
// EventGatewayType selects an instantiating gate's start policy (ADR-005 v.4 §2.12.4).
// It is meaningful only with WithInstantiate; a non-instantiating (mid-flow) gate is
// always Exclusive (BPMN §10.6.6).
type EventGatewayType uint8

const (
	ExclusiveEvents EventGatewayType = iota // each event → a new instance (default)
	ParallelEvents                          // first event → one instance; wait for all
)

type EventBasedGateway struct {
	corrKey *bpmncommon.CorrelationKey // gate-level correlation (Parallel-start)
	Gateway
	instantiate bool
	gwType      EventGatewayType
}

func WithInstantiate() EventBasedOption              // mark the gate a start instantiator
func WithEventGatewayType(t EventGatewayType) EventBasedOption
func WithCorrelationKey(k *bpmncommon.CorrelationKey) EventBasedOption

func (g *EventBasedGateway) Instantiate() bool
func (g *EventBasedGateway) EventGatewayType() EventGatewayType
func (g *EventBasedGateway) CorrelationKey() *bpmncommon.CorrelationKey
func (g *EventBasedGateway) ParallelStart() bool // instantiate && gwType == ParallelEvents
```

(`EventBasedOption`/машинерия конфигурации, удалённая в переработке §10.6.6 из SRD-024,
минимально переиспользуется для этих опций; зеркалит `complex.go`/прежнюю форму.
`WithCorrelationKey` несёт **один** `CorrelationKey`, чьё `CorrelationProperty` держит
retrieval-выражение на каждое сообщение arm'а, так что стартер выводит один и тот же
ключ беседы из того arm'а, что сработал первым — BPMN §8.4.2: message-триггеры шлюза
«разделяют одну и ту же корреляционную информацию».)

### 3.2 Born-seeding для Parallel-start (`internal/instance`)

**Поле completion-gate не нужно** — завершение *автоматическое* (см. §4.3). Born-path
для Parallel-start `seedParallelStart` (`instance.go:1018`) предварительно срабатывает
сработавший arm (track на его исходящем, через `ArmFor`) и засевает **ожидающий track на
каждом из остальных arm-узлов**. Track в состоянии `TrackWaitForEvent` держит счётчик
`active` экземпляра `> 0` (цикл делает `active++` на каждый запущенный track и `active--`
только на `evEnded`, `instance.go`), так что существующая проверка завершения в
`instance.go:667` — `active == 0` — уже блокируется, пока не сработает каждый arm и не
исполнит своё продолжение. Засеянный ключ беседы делает ожидающие keyed на `K`
(`CorrelationKeys()`), так что последующие arm'ы маршрутизируются в них.

```go
// createTracks gains a Parallel-start branch: seedParallelStart(gate, bornEvent)
// pre-fires the instantiating arm and arms the rest as keyed waiters. The other arms'
// waiting tracks keep active>0 until they fire — no separate eventGate.expected field.
func (inst *Instance) seedParallelStart(gate flow.Node, bornEvent flow.EventDefinition) error
```

---

## 4. Анализ

### 4.1 Распознавание инстанцирующего шлюза

`isInstantiatingStartNode` (`instance_starter.go:146`) добавляет: `EventBasedGateway`,
чей `Instantiate()` истинен. `scanInstantiatingStarts` (`:99`) — для такого шлюза (без
входящих) — строит **один** стартер, регистрирующийся на определении сообщения
**каждого arm'а** (`Definitions()` шлюза), так что любой arm срабатывает его. Стартер
запоминает шлюз + `eventGatewayType`, так что `ProcessEvent` выбирает Exclusive vs
Parallel.

### 4.2 Exclusive-start

`ProcessEvent` стартера для Exclusive-шлюза вызывает `resolveAndLaunch` ровно так же, как
message-start — рождённый из шлюза, маршрутизированный в сработавший arm (экземпляр
исполняется с продолжения этого arm'а; узел шлюза записывается, затем маршрутизируется,
переиспользуя `ArmFor` из SRD-024). Каждое событие независимо; дедупликация `seenKeys`
применяется только если arm'ы несут ключ (обычно arm'ы Exclusive-start не разделяют его).

### 4.3 Parallel-start

Первый arm (ключ `K`) инстанцирует (рождённый из шлюза, засеянный `K`). При рождении
`seedParallelStart` (`instance.go:1018`) **предварительно срабатывает сработавший arm**
(track на его исходящем, разрешённый через `ArmFor`) и **засевает ожидающий track на
каждом из остальных arm-узлов шлюза**; засеянный ключ беседы делает эти ожидающие keyed
на `K` (`CorrelationKeys()`). Сообщение последующего arm'а (ключ `K`) маршрутизируется
правилом **наибольшей специфичности** membroker'а (`pkg/messaging/membroker/membroker.go:128`
— «keyed-подписка … предпочтительнее wildcard») в keyed-ожидающий *этого* экземпляра
предпочтительнее, чем в wildcard-стартер определения; он срабатывает этот arm и
форкает его продолжение. (Даже если стартер тоже увидел его, дедупликация `seenKeys`
делает это no-op — исходы складываются.)

**Завершение автоматическое** — ожидающий track каждого ещё не сработавшего arm'а держит
`active > 0`; экземпляр достигает `active == 0` (`instance.go:667`) только после того, как
сработал каждый arm и исполнил своё продолжение. Поэтому поле `eventGate.expected` не
нужно (упрощение дизайна против черновика v.1 — см. §10). Seen-key путь `resolveAndLaunch`
остаётся no-op для инстанцирования (последующее сообщение достигает экземпляра напрямую
через фазу 2c).

### 4.4 Почему нет нового ADR

ADR-005 v.4 §2.12.3 (Parallel completion-gate, проверено) и §2.12.4 (инстанциатор,
Exclusive vs Parallel) **решают** это; ADR-015 (born-from-event) + ADR-016 (key-корреляция,
фаза 2b/2c) поставляют механику. Этот SRD связывает их и **снимает отложение ADR-015
§2.6** (синхронизация связанного дока, не правка).

---

## 5. Тестовые сценарии (§6)

| # | Тест | Сценарий | Проверяет |
|---|---|---|---|
| 1 | `TestEventGatewayExclusiveStart` | инстанцирующий Exclusive шлюз, 2 message-arm'а; публикуем arm A | новый экземпляр исполняет путь arm'а A до завершения; нет ожидания B |
| 2 | `TestEventGatewayExclusiveStartEachEventNewInstance` | публикуем два (нескоррелированных) события arm'ов | два независимых экземпляра |
| 3 | `TestEventGatewayParallelStartCompletesOnAll` | инстанцирующий Parallel шлюз, 2 скоррелированных arm'а; публикуем A затем B (тот же ключ) | первый создаёт один экземпляр; B маршрутизируется в него; экземпляр завершается только после **обоих** |
| 4 | `TestEventGatewayParallelStartDoesNotCompleteEarly` | публикуем только A | экземпляр остаётся Active (завершение заблокировано несработавшим arm'ом) до прихода B |
| 5 | `TestEventGatewayParallelStartCorrelation` | два ключа (K1, K2), arm'ы вперемешку | каждый экземпляр видит только arm'ы своего ключа; без перекрёстных помех |
| 6 | `TestEventBasedGatewayValidate` (+ `TestEventBasedConfigValidate`, `TestEventBasedGatewayValidateReceiveArmBoundary`) | Parallel без instantiate / инстанцирующий шлюз с входящим потоком / non-message arm на старте / boundary у receive-arm | каждое отклонено при регистрации |
| 7 | model-unit (`TestEventBasedGatewayParallelStartAndKey`, `TestWithCorrelationKeyNil`, `TestEventBasedGatewayArmForMessageByName`, …) | `WithInstantiate`/`WithEventGatewayType`/`WithCorrelationKey`, `Instantiate`/`EventGatewayType`/`CorrelationKey`/`ParallelStart`, `Clone`, `defMatches` по имени | конструирование + перенос + сопоставление сообщения по имени |

In-package тесты (`internal/instance`) покрывают completion-gate для по-пакетного
покрытия.

---

## 8. Cross-doc

- **Реализует** [ADR-005 v.4](../design/ADR-005-gateways-and-joins.ru.md) §2.12.3/§2.12.4 (вверх).
- [ADR-015 v.1](../design/ADR-015-event-triggered-instantiation.ru.md) — born-from-event;
  отложение §2.6 снято здесь (вверх).
- [ADR-016 v.1](../design/ADR-016-message-correlation.ru.md) §2.3/§2.4 — key-дедупликация +
  conversation-token threading / маршрутизация по наибольшей специфичности (вверх).
- [SRD-024 v.1](SRD-024-event-based-gateway.ru.md) — шлюз середины потока, который это расширяет (вбок).
- [SRD-017 v.1](SRD-017-conversation-token-threading.ru.md) — переиспользуемая маршрутизация фазы 2c (вбок).

(Версии зафиксированы при авторинге; нисходящих ссылок нет.)

## 9. Definition of Done

- FR-1…FR-7 связаны; тесты §5 проходят под `-race`.
- `make ci` зелёный: lint, build, `-race`, diff-покрытие ≥95% (цель 100%), govulncheck.
- `examples/` получает пример Parallel-start (напр., заказ, открываемый любым из двух
  скоррелированных сообщений, завершающийся по приходу обоих), smoke exit 0.
- Заметка об отложении ADR-015 §2.6 обновлена (синхронизация связанного дока) по приземлении.
- **Вне области:** Conditional arm'ы (нужен conditional-waiter); context-based корреляция
  §2.5 (фаза 3, отложено); перевооружение в циклах (engine-wide).

## 10. Сводка реализации

Приземлено в ветке `feat/event-based-instantiator` (от `master`).

### 10.1 Этапы по коммитам

| Веха | Коммит | Объём | Тесты |
|---|---|---|---|
| Док | `9204e17` | SRD-025 (этот док) | — |
| M1 — стартовые атрибуты + валидация | `577b117` | `EventBasedGateway` `WithInstantiate`/`WithEventGatewayType`/`Instantiate()`/`EventGatewayType()` + правила старта `Validate` (`event_based.go`) | `TestEventBasedGatewayValidate`, `TestEventBasedConfigValidate` |
| M2 — Exclusive-start | `28af691` | ветка шлюза в `scanInstantiatingStarts` (`startNode = arm` через `ArmFor`); born-path переиспользован (`instance_starter.go`) | `TestEventGatewayExclusiveStart`, `…EachEventNewInstance` |
| M3 — Parallel-start (+ M3a, + 2 фикса) | `0e1f1a9` | `WithCorrelationKey`/`CorrelationKey()`/`ParallelStart()` на шлюзе; `seedParallelStart` (засев ожидающих track'ов); ветка Parallel в `scanInstantiatingStarts` (`startNode = gate`, общий ключ); `defMatches` по имени; handle в `launchInstanceFromEvent` | `…ParallelStartCompletesOnAll`, `…DoesNotCompleteEarly`, `…Correlation`, `TestSeedParallelStart*`, `TestEventBasedGatewayParallelStartAndKey`, `TestWithCorrelationKeyNil`, `TestEventBasedGatewayArmForMessageByName` |

Ветка также несёт два сквозных коммита, вложенных по просьбе пользователя (не вехи
SRD-025): `90d4f98` (примеры печатают свою схему процесса) и `2f22881` (Debug-логирование
обработки событий через EventHub / membroker / стартер).

### 10.2 Отличия от черновика v.1

- **Completion-gate — автоматический, без поля `eventGate` (FR-6 / §3.2 / §4.3).** Черновик
  предлагал поле `eventGate{expected map[...]}`, очищаемое по каждому сработавшему arm'у.
  Реализация засевает ещё не сработавшие arm'ы шлюза как **ожидающие track'и**
  (`seedParallelStart`, `instance.go:1018`), которые держат `active > 0` до срабатывания,
  так что существующее завершение по `active == 0` (`instance.go:667`) уже блокируется на
  всех arm'ах. Без нового поля — упрощение. (Проверено `…DoesNotCompleteEarly`: один arm
  сработал ⇒ экземпляр остаётся Active, пока не придёт второй.)
- **`CorrelationKey` уровня шлюза (M3a, FR-1 / FR-2).** Необходимая предпосылка, которую
  черновик v.1 только предполагал (§4.3): `WithCorrelationKey`/`CorrelationKey()` добавлены
  на шлюз (`event_based.go`) — иначе объявление корреляции есть только у
  `StartEvent`/`SendTask`; у arm'ов шлюза (промежуточный catch / receive task) его нет.
  Один ключ шлюза с retrieval-выражением на каждое сообщение arm'а (BPMN §8.4.2).
- **`defMatches` по имени сообщения (баг исправлен по ходу).** `ArmFor` сопоставлял
  сообщения по id определения, но `Clone()` даёт arm'ам экземпляра свежие id определений,
  так что Parallel-start шлюз никогда не разрешал свой сработавший arm. `defMatches`
  (`event_based.go:283`) теперь сопоставляет `MessageEventDefinition` и по **имени**
  (зеркаля fallback по имени для сигналов).
- **`InstanceHandle` для рождённых из события (существовавший баг исправлен по ходу).**
  `launchInstanceFromEvent` (`thresher.go:694`/`:769`) не регистрировал handle, так что
  `Thresher.Instance(id)` возвращал nil, а `WaitCompletion`/`State` ломались для **каждого**
  рождённого из события экземпляра (не только Parallel) — латентный пробел SRD-015/019.
  Теперь регистрирует handle, как `launchInstance`.

### 10.3 Верификация (V-результаты)

- `make ci` зелёный на HEAD: tidy, lint, build, `-race`, **diff-покрытие 98.8%**
  (`COVER_MIN` 95), govulncheck чисто.
- Все 15 примеров `examples/` smoke зелёные (exit 0), включая новый
  `examples/event-based-parallel-start`.
- Тесты §5 проходят под `-race`.

## Открытые вопросы

- **Нет.**
