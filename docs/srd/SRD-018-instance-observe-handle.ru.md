# SRD-018 — Наблюдение за instance: публичный handle + поток событий жизненного цикла

| Поле | Значение |
|---|---|
| Статус | Принято |
| Версия | v.1 |
| Дата | 2026-06-18 |
| Владелец | Руслан Габитов |
| Реализует | [ADR-013 v.1 Instance Observability & Control](../design/ADR-013-instance-observability.md) |

> EN-оригинал — канонический: [SRD-018-instance-observe-handle.md](SRD-018-instance-observe-handle.md). Этот файл — его перевод (twin). При расхождении приоритет у английского текста.

Этот SRD приземляет **observe**-срез [ADR-013 v.1](../design/ADR-013-instance-observability.md)
(§2.1 поверхность наблюдения, §2.2 единый канал lifecycle/token/node + асинхронная
lossy-доставка, §2.4 стандартно-названный открытый словарь состояний). Он даёт
host'у публичный **`InstanceHandle`**, возвращаемый из `StartProcess` —
`State()`, снимок позиций токенов, read-only-данные и `WaitCompletion(ctx)` —
плюс единый **поток событий**, на который подписываются observer'ы и в который
event loop instance и его track'и публикуют события lifecycle / token-movement /
node-progress. **Управление** (`Cancel`, `Suspend`/`Resume`) и **жизненный цикл
движка** (`Shutdown`, `UnregisterProcess`) — это *другой* срез ADR-013 и **вне
объёма** здесь (sibling-SRD).

## 1. Контекст и мотивация

### 1.1 Текущее состояние (проверено по коду)

- **Публичный API write-only (audit 2.2).** `Thresher.StartProcess(processID string) error`
  (`pkg/thresher/thresher.go:622`) возвращает только `error` — ни handle, ни
  состояния, ни токенов, ни сигнала завершения. Host, стартовавший процесс, не
  может узнать ничего больше.
- **Работающие instance отслеживаются, но недостижимы.** Thresher держит
  `instances map[string]instanceReg` (`thresher.go:109`), где
  `instanceReg{stop context.CancelFunc; inst *instance.Instance}` (`thresher.go:98-101`),
  под `sync.Mutex m` (`thresher.go:113`); `launchInstance` сохраняет
  `t.instances[inst.ID()]`, но ничего не возвращает вызывающему.
- **Instance уже выставляет наблюдение внутренне — lock-free.**
  `Instance.State() State` (`internal/instance/instance.go:501`) читает
  `atomic.Uint32` (`instance.go:100`); enum состояний —
  `Created → Active → Completed` (+ `Terminating → Terminated`)
  (`instance.go:48-67`). `GetTokens() []Token` (`instance.go:720`) и
  `TokenHistory() []TokenPath` (`instance.go:740`) читают copy-on-write
  `tracksSnap atomic.Pointer[[]*track]` (`instance.go:91`) без локов.
  **Но `internal/instance` закрыт для импорта внешними host'ами** (правило Go
  `internal/` + ADR-012), так что ничего из этого недостижимо из потребителя
  `pkg/thresher`.
- **Токен — это проекция, не хранимая сущность.** `track.Token() Token`
  (`internal/instance/track.go:191`), где `Token{Node flow.Node; State TokenState}`
  (`token.go`); `TokenState` ∈ `Alive / WaitForEvent / Consumed / Withdrawn`.
- **Нет сигнала завершения и нет host-facing-observer'а.** У instance есть
  внутренний `events chan trackEvent` (`instance.go:87`) и `loopDone chan
  struct{}` (`instance.go:96`), плюс `lastErr atomic.Pointer[error]`
  (`instance.go:92`) — всё неэкспортируемое. Примеры узнают о завершении только
  протаскивая ручной `done := make(chan struct{})` через service-functor, или
  через `time.Sleep`.
- **Read-only-ридер данных уже есть, публичный.** `service.DataReader`
  (`pkg/model/service/datareader.go:10-24`): `GetData(name)`, `GetDataByID(id)`,
  `GetSources()`, `List(path)`; его подкрепляет `dataPlane *scope.Scope` instance.

### 1.2 Проблема

Host, встраивающий gobpm, слеп после `StartProcess`: он не может прочитать
состояние, увидеть где токены, прочитать данные процесса, заблокироваться до
завершения или следить за прогрессом вживую — только читать логи. ADR-013 решил,
как это чинить; этот SRD реализует **observe**-половину без утечки god-object'а
`internal/instance`.

## 2. Решение

Возвращать публичный **`thresher.InstanceHandle`** из `StartProcess` (плюс
поиск по id). Handle — это **узкое окно** над внутренним instance: он держит
`*instance.Instance` неэкспортируемым и выставляет только read-only-наблюдение,
никогда мутирующую поверхность god-object'а (ADR-013 §4). Он предоставляет:

- **pull**-наблюдение — `State()`, `Tokens()` (живые позиции), `History()`
  (путь каждого track'а, вкл. merged/consumed, с lineage + тайминги шагов),
  `Data()` — поверх существующих lock-free-аксессоров instance; и
  `WaitCompletion(ctx)`, подкреплённый закрытием `loopDone` + `lastErr`.
- **push**-наблюдение — `Observe(...)`, регистрирующий observer'а на **одном**
  потоке событий, в который event loop instance и его track'и публикуют события
  lifecycle / token-movement / node-progress, с **асинхронной best-effort lossy**
  доставкой (буферизованный канал на observer'а + горутина дренажа + неблокирующая
  отправка + счётчик drop'ов), так что медленный observer никогда не блокирует
  track, а терминальное завершение никогда не теряется.

Публичный словарь состояний/токенов **стандартно-назван и открыт** (ADR-013 §2.4):
код host'а обязан терпеть неизвестные значения (forward-compatible).

## 3. Функциональные требования

**Observe-by-pull (handle):**

- **FR-1 — `StartProcess` возвращает handle.** `StartProcess(processID string)
  (*InstanceHandle, error)` (было только `error`, `thresher.go:622`).
- **FR-2 — поиск по id.** `Thresher.Instance(instanceID string) (*InstanceHandle,
  bool)` возвращает handle отслеживаемого работающего instance, или `false`.
- **FR-3 — `State()`.** `InstanceHandle.State() InstanceState` маппит внутренний
  `instance.State` (`instance.go:501`) в публичный, стандартно-названный открытый
  словарь `InstanceState`. Lock-free.
- **FR-4 — снимок живых токенов.** `InstanceHandle.Tokens() []TokenView` проецирует
  `GetTokens()` (`instance.go:720`, только активные track'и — `Alive` /
  `WaitForEvent`) в публичный `TokenView{NodeID, NodeName string; State TokenState}`.
  Lock-free.
- **FR-4a — полная история токенов.** `InstanceHandle.History() []TokenPath`
  проецирует `TokenHistory()` (`instance.go:740`) в публичный `TokenPath{TrackID,
  ParentID string; Steps []StepVisit; Terminal TokenState}` — **каждый** track,
  активный и завершённый, с lineage (`ParentID` = fork-родитель) и таймингами
  визитов по шагам. Это вид «включая merged-токены»; `Tokens()` (FR-4) остаётся
  снимком живых. **Заметка по словарю (обоснована):** проекция сворачивает
  ended / **merged** / canceled / failed track'и в **`Consumed`** (`token.go`
  `tokenStateFor`) — отдельного значения `Merged` нет; merged-track появляется как
  `Consumed`-`TokenPath`, чьи `Steps` заканчиваются на join-узле, а `ParentID`
  раскрывает fork-lineage. Публичный `TokenState` — проецируемое множество
  `Alive / WaitForEvent / Consumed / Withdrawn`. Lock-free. Без варианта на один
  track — полный список фильтруется по `TrackID`.
- **FR-5 — read-only-данные.** `InstanceHandle.Data() service.DataReader` выставляет
  data plane instance (`instance.go:88`) read-only: process properties +
  RUNTIME-переменные (`StartedAt/CurrState/TracksCount`). Без мутирующей поверхности.
- **FR-6 — `WaitCompletion`.** `InstanceHandle.WaitCompletion(ctx context.Context)
  (InstanceState, error)` блокируется до терминального состояния
  (`Completed`/`Terminated`) или `ctx`; возвращает терминальное состояние + любой
  `lastErr` (`instance.go:92`).

**Observe-by-stream (единый канал):**

- **FR-7 — регистрация observer'а.** `InstanceHandle.Observe(o Observer)
  *Subscription` регистрирует observer'а на единый поток событий instance;
  возвращённый `Subscription` снимает регистрацию + дренирует через `Cancel()` и
  сообщает число drop'ов через `Dropped()`. `Observer` — одно-методный интерфейс
  (`OnEvent(Event)`).
- **FR-8 — события потока.** `Event{Kind EventKind; InstanceID, NodeID, NodeName,
  State string; At time.Time}` для: lifecycle instance, token-movement,
  node-execution-прогресса — источники на переходах состояния event loop и точках
  `record()` track'ов (`track.go:171`). **Без payload'ов** — только id/имена/
  состояние/timestamp'ы (masking, ADR-010/011).
- **FR-9 — асинхронная best-effort lossy-доставка.** На observer'а:
  **буферизованный канал** (размер N), дренируемый **одной выделенной горутиной**,
  вызывающей `OnEvent`; emitter отправляет **неблокирующе**
  (`select { case ch <- ev: default: dropped++ }`). Track/loop никогда не
  блокируется на observer'е; медленный observer **теряет** события и выставляет
  счётчик `Dropped() uint64`; паникующий observer recover'ится. **Терминальное
  завершение никогда не теряется** — это закрытие `loopDone` через `WaitCompletion`
  (FR-6), независимое от lossy-потока.

## 4. Нефункциональные требования

- **NFR-1 — нет утечки god-object'а.** Handle выставляет только read-only-наблюдение;
  никогда не возвращает `*instance.Instance` и не имеет мутирующих методов (ADR-013
  §4, ADR-012). `internal/instance` остаётся internal.
- **NFR-2 — наблюдение вне hot path.** Pull-чтения используют существующие
  lock-free-аксессоры (atomics / copy-on-write-снимок); push-эмиссия — неблокирующая
  отправка; новый mutex на пути исполнения track'а не вводится.
- **NFR-3 — forward-compatible-словарь.** `InstanceState` / `TokenState` /
  `EventKind` — открытые множества со `String()`; потребители обязаны терпеть
  неизвестные значения. Добавление состояния позже неломающее (ADR-013 §2.4).
- **NFR-4 — покрытие.** Каждый созданный/обновлённый файл финиширует ≥80% (цель
  100%) diff-покрытия; `make ci` зелёный.

## 5. Анализ путей (альтернативы)

- **Возвращать handle из `StartProcess` vs оставить `error`-only + отдельный
  `Instance(id)`-поиск.** Выбрано: **оба** — возврат на старте (частый путь) +
  поиск по id (FR-2). Отклонено error-only: гонит каждый host через второй вызов
  за тем, что он только что создал.
- **Выставить `*instance.Instance` / его экспортированные методы напрямую.**
  Отклонено: `internal/instance` закрыт для host'ов, и тип — god-object с
  мутирующими методами (ADR-013 §4).
- **Переиспользовать `instance.State` как публичный тип.** Невозможно — он под
  `internal/`. Нужен публичный `thresher.InstanceState`, что и делает возможным
  forward-compat открытого словаря.
- **Доставка observer'у: буфер-drop-newest + счётчик (выбрано) vs drop-oldest-ring
  vs синхронный callback vs неограниченная очередь.** ADR-013 §2.2 решил
  drop-newest + счётчик как устойчивый дефолт; синхронный callback дал бы
  медленному observer'у блокировать track (запрещено); неограниченная очередь —
  риск раздувания памяти.
- **Канал-возвращающий `Subscribe() <-chan Event` vs `Observe(Observer)`-callback.**
  Выбрано `Observe(Observer)` + внутренняя горутина дренажа: движок владеет
  политикой буфер/drop/recover (FR-9), а не выставляет сырой канал.

## 6. API (публичная поверхность, `pkg/thresher`)

```go
// InstanceHandle is a read-only window onto one running process instance.
// It never exposes the engine's internal instance object or any mutating method.
type InstanceHandle struct{ /* unexported: inst *instance.Instance */ }

func (h *InstanceHandle) ID() string
func (h *InstanceHandle) State() InstanceState
func (h *InstanceHandle) Tokens() []TokenView   // live active positions (Alive/WaitForEvent)
func (h *InstanceHandle) History() []TokenPath  // every track incl. merged/consumed, lineage + timings
func (h *InstanceHandle) Data() service.DataReader
func (h *InstanceHandle) WaitCompletion(ctx context.Context) (InstanceState, error)
func (h *InstanceHandle) Observe(o Observer) *Subscription

// Subscription is a live observer registration.
type Subscription struct{ /* unexported */ }
func (s *Subscription) Cancel()         // deregister + drain + stop
func (s *Subscription) Dropped() uint64 // events dropped when the observer fell behind

// InstanceState is the standard-named, OPEN lifecycle vocabulary (ADR-013 §2.4).
type InstanceState string
const (
	StateCreated     InstanceState = "Created"
	StateActive      InstanceState = "Active"
	StateCompleted   InstanceState = "Completed"
	StateTerminating InstanceState = "Terminating"
	StateTerminated  InstanceState = "Terminated"
)

type TokenView struct {
	NodeID   string
	NodeName string
	State    TokenState // "Alive" | "WaitForEvent" | "Consumed" | "Withdrawn" (open)
}
type TokenState string

// TokenPath is one track's full path — including merged/consumed/canceled tracks.
type TokenPath struct {
	TrackID  string
	ParentID string      // immediate fork parent ("" if root)
	Steps    []StepVisit
	Terminal TokenState  // projected: Consumed for ended/merged/canceled, else the live state
}
type StepVisit struct {
	NodeID   string
	NodeName string
	State    TokenState
	At       time.Time
}

type Observer interface{ OnEvent(Event) }

type Event struct {
	Kind       EventKind
	InstanceID string
	NodeID     string
	NodeName   string
	State      string
	At         time.Time
}
type EventKind string // "InstanceState" | "NodeProgress" | "TokenMoved" (open)

// Engine entry point (changed return):
func (t *Thresher) StartProcess(processID string) (*InstanceHandle, error)
func (t *Thresher) Instance(instanceID string) (*InstanceHandle, bool)
```

## 7. План тестирования

- **`TestStartProcessReturnsHandle`** — `StartProcess` даёт ненулевой handle с
  совпадающим `ID()`; `Instance(id)` находит (FR-1, FR-2).
- **`TestWaitCompletion`** — `WaitCompletion` возвращает `Completed`; отменённый
  `ctx` возвращает свою ошибку, пока instance ещё работает (FR-3, FR-6).
- **`TestTokensSnapshot`** — `Tokens()` показывает живой токен на исполняющемся
  узле (FR-4).
- **`TestHistoryIncludesMerged`** — fork+join даёт `History()`-записи для merged
  track'ов (терминал `Consumed`, `Steps` на join, `ParentID`-lineage) плюс
  выжившего (FR-4a).
- **`TestHandleDataRead`** — `Data().GetData` читает RUNTIME-переменную read-only
  (FR-5).
- **`TestObserverReceivesLifecycleEvents`** — observer видит lifecycle + node-события
  (FR-7, FR-8).
- **`TestSlowObserverDropsNeverBlocks`** — заблокированный observer теряет события
  (`Dropped() > 0`), пока instance всё равно завершается (FR-9).
- **`TestObserverPanicRecovered`** — паникующий `OnEvent` не роняет движок и не
  голодит здорового peer'а (FR-9).
- **Обновление примера** — `examples/basic-process` использует `WaitCompletion`
  вместо ручного `done`-канала; smoke exit 0.

## 8. Кросс-документная согласованность

- **Реализует** [ADR-013 v.1](../design/ADR-013-instance-observability.md) §2.1/§2.2/§2.4.
  §2.3/§2.5 (управление + жизненный цикл движка) — sibling-SRD.
- [ADR-011 v.5 §2.6](../design/ADR-011-process-data-flow.md) — публичный
  `service.DataReader`, который возвращает `Data()`.
- [ADR-012 v.1](../design/ADR-012-execution-layering.md) — граница
  публичный-`pkg`/internal, которую handle соблюдает.
- [ADR-001 v.5](../design/ADR-001-execution-model.md) — состояния жизненного цикла
  instance + single-owner loop, по которому едет эмиссия.
- [ADR-002 v.2](../design/ADR-002-extension-architecture.md) — поверхность
  публичного API §4.7, к которой присоединяется handle.
- Ссылки вверх/вбок, version-pinned; нисходящих ссылок нет.

## 9. Definition of Done

- FR-1…FR-9 (вкл. FR-4a `History()`) подключены и проверены тестами §7.
- `StartProcess` возвращает `*InstanceHandle`; все call-site (примеры, тесты)
  обновлены.
- Handle не выставляет мутирующую поверхность и не возвращает `*instance.Instance`
  (NFR-1).
- `make ci` зелёный (tidy, lint вкл. fieldalignment, build, `-race`, diff-coverage
  ≥95 на изменённых строках, govulncheck); touched-файлы ≥80% (цель 100%).
- `examples/basic-process` smoke-запускается (exit 0) через `WaitCompletion`.
- §10 заполнена; статус → Принято; добавлен RU-twin; linked-доки синхронизированы.

## 10. Сводка реализации

### 10.1 Коммиты (ветка `feat/instance-observability`)

| Стадия | Коммит | Объём |
|---|---|---|
| doc | `79fdfee` | SRD-018 |
| M1 | `c7c5fe7` | pull-handle — `InstanceHandle` (`State`/`Tokens`/`History`/`Data`/`WaitCompletion`), `StartProcess`→handle, поиск `Thresher.Instance`, `Done()`/`DataReader()` + lifetime root-ридер, обновления call-site + примера |
| M2 | `85357eb` | поток событий — `Observe`/`*Subscription`, асинхронная lossy-доставка (буфер-дренаж + счётчик drop'ов + panic-recover), fan-out `ObsEvent` из `setState`+`record`, `String()` у `TokenState`/`ObsKind` |

### 10.2 Ключевые файлы

- `pkg/thresher/handle.go` — `InstanceHandle` + публичные типы словаря.
- `pkg/thresher/observer.go` — `Observer`/`Event`/`EventKind`/`Subscription` + `Observe`.
- `internal/instance/observer.go` — `ObsEvent`/`ObsKind` + `AddObserver`/`removeObserver`/`notify`.
- `internal/instance/instance.go` — `Done()`/`DataReader()`, lifetime-ридер (в `loadProperties`), эмиссия `setState`; `pkg/thresher/thresher.go` `StartProcess`/`launchInstance`/`Instance`.
- `internal/instance/track.go` — эмиссия `record()`.

### 10.3 V-результаты

- `make ci` зелёный: lint (вкл. fieldalignment, misspell), build, `-race`,
  **diff-coverage 98.2%** из 222 изменённых строк (≥95), govulncheck чист.
- Все тесты §7 зелёные; все 9 примеров smoke exit 0 (`examples/basic-process`
  переведён на `WaitCompletion`); README quick-start обновлён под handle +
  пример finish-listening/observer.

### 10.4 Отличия от черновика

- **Словарь токенов** — проекция сворачивает ended/merged/canceled в `Consumed`
  (отдельного `Merged` нет); `History()` показывает merged-track'и как `Consumed`
  с их join-путём + lineage (§4.1a уточнён до реализации).
- **Сигнатура `Observe`** — возвращает `*Subscription` (`Cancel()`/`Dropped()`),
  а не голый `cancel func()`, поскольку FR-9 выставляет счётчик drop'ов (§6
  согласован).
- **Ридер данных** — стал **lifetime**-ресурсом instance, строящимся однажды в
  `loadProperties` (так что `DataReader()` и handle без error-пути).
- **Без изменений**: ~4 недостижимых защитных guard'а ошибок в
  `launchInstance`/`loadProperties` (пре-валидированный snapshot + свежий instance
  + открытый scope не дают им сработать) объясняют <100% diff-покрытия; принято как
  детерминированные 98.2%.

## История документа

| Версия | Дата | Автор | Изменение |
|---|---|---|---|
| v.1 | 2026-06-18 | Руслан Габитов | Принято (приземлено через M1 `c7c5fe7` + M2 `85357eb`, `make ci` зелёный, diff-coverage 98.2%). Приземляет observe-срез ADR-013 v.1 (§2.1/§2.2/§2.4): публичный `thresher.InstanceHandle` из `StartProcess` (pull: `State`/`Tokens` живые + `History` вкл. merged/`Data`/`WaitCompletion`) + один асинхронный best-effort lossy-поток событий (`Observe`); стандартно-названный открытый словарь состояний/токенов/событий. Управление + жизненный цикл движка (§2.3/§2.5) вне объёма, отложены в sibling-SRD. Code-grounded по `pkg/thresher`, `internal/instance`, `pkg/model/service`. Реализует ADR-013 v.1; ссылки ADR-001 v.5, ADR-002 v.2, ADR-011 v.5, ADR-012 v.1. |
