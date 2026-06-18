# SRD-019 — Управление экземпляром и жизненный цикл движка: Cancel, Shutdown, дренаж waiter'ов

| Поле | Значение |
|---|---|
| Статус | Принято |
| Версия | v.1 |
| Дата | 2026-06-18 |
| Владелец | Руслан Габитов |
| Реализует | [ADR-013 v.1 Instance Observability & Control](../design/ADR-013-instance-observability.ru.md) |

Этот SRD приземляет срез **управления + жизненного цикла движка** из
[ADR-013 v.1](../design/ADR-013-instance-observability.ru.md) (§2.3 грубое
управление, §2.5 жизненный цикл движка) и **реализует ещё открытую часть
[ADR-006 v.1 §2.5](../design/ADR-006-events-and-subscriptions.ru.md)** —
синхронизированную через `WaitGroup` остановку waiter'ов EventHub, которую
потребляет `Thresher.Shutdown`. Это близнец уже влитого среза наблюдения
([SRD-018 v.1](SRD-018-instance-observe-handle.ru.md) — pull-ручка + поток
событий наблюдателя). Грубое, явное, опосредованное движком управление:
`InstanceHandle.Cancel` (+ зарезервированные `Suspend`/`Resume`),
`Thresher.Shutdown`, `Thresher.Forget` и уже существующий `UnregisterProcess`,
задокументированный и слегка укреплённый.

## 1. Контекст и мотивация

### 1.1 Текущее состояние (проверено по коду)

- **Нет способа отменить работающий экземпляр.** `InstanceHandle`
  (`pkg/thresher/handle.go:16`) только для чтения (SRD-018:
  `ID/State/Tokens/History/Data/WaitCompletion/Observe`); у него нет `Cancel`.
  У экземпляра **нет собственной отмены и метода `Cancel()`/`Terminate()`** —
  завершение управляется только **ctx-cancel**: `Instance.Run(ctx)` сохраняет
  `inst.ctx = ctx` (`internal/instance/instance.go:558-585`); цикл наблюдает
  `ctx.Done()` → `stopAll()` → `setState(Terminating)` → остановка треков →
  `setState(Terminated)` + `close(loopDone)` (`instance.go:603-688`).
  Единственный держатель отмены экземпляра — Thresher: `launchInstance` делает
  `ctx, cancel := context.WithCancel(t.ctx)` и хранит `instanceReg{stop: cancel, …}`
  (`thresher.go:679,696`; `instanceReg` в `thresher.go:97-102`) — сегодня не
  вызывается.
- **Нет `Thresher.Shutdown`.** Перечисление `State` — `Invalid / NotStarted /
  Started / Paused` (`thresher.go:58-95`) — **нет `Stopped`**. `Run(ctx)`
  выставляет `Started`, синхронно вызывает `eventHub.Start(ctx)`, затем
  поднимает `go eventHub.Run(ctx)` (`thresher.go:245-299`); `StartProcess`
  проверяет `st != Started` (`thresher.go:623-647`). **Грациозной остановки
  нет**: нет Thresher `Shutdown`/`Stop`.
- **У EventHub нет shutdown и нет `WaitGroup`** (открытый пункт ADR-006 §2.5).
  `EventHub` (`internal/eventproc/eventhub/eventhub.go:31-38`) держит `waiters
  map[string]EventWaiter` + `m sync.RWMutex` + `started`; **нет `sync.WaitGroup`,
  нет `Shutdown`/`Close`**. `Run(ctx)` просто блокируется на `<-ctx.Done()`
  (`eventhub.go:86-96`). `Service(ctx)` каждого waiter'а поднимает фоновую
  go-рутину (таймер: `waiters/timer.go:245-276`), а `Stop()` сигналит ей
  (`timer.go:369-385`). Ничто **не ждёт** выхода этих го-рутин при остановке →
  остановка оставляет живые го-рутины.
- **Единоличное владение waiter'ами хабом уже на месте** (FIX-003): waiter
  сообщает о терминальном срабатывании через `hub.WaiterFired(eDefID)` и **не
  удаляет себя сам**; удаление принадлежит хабу (`eventhub.go:343` `WaiterFired`,
  `:318` `RemoveWaiter`, `:214` `UnregisterEvent`). Дефекты double-close / TOCTOU /
  double-removal (аудит 1.3/1.5/2.5-ownership) **исправлены**; от ADR-006 §2.5
  остался **синхронизированный дренаж**.
- **`UnregisterProcess` существует, но не учитывает живые экземпляры**
  (`thresher.go:450-491`): удаляет записи `snapshots`/`starters` и снимает
  регистрацию стартеров в хабе, **без проверки** работающих экземпляров этого
  процесса. Карта `snapshots` освобождается только здесь (аудит 2.2). Карта
  `instances` (`thresher.go:651` поиск `Instance(id)`) **никогда не чистится** —
  завершённые экземпляры накапливаются.

### 1.2 Проблема

Хост может наблюдать работающий экземпляр (SRD-018), но не может **воздействовать**:
нет отмены, нет грациозного завершения движка, нет способа освободить завершённый
экземпляр, и EventHub утекает waiter-го-рутины при остановке (открыт ADR-006 §2.5).
Этот SRD добавляет грубое, явное, опосредованное движком управление + жизненный
цикл, решённые в ADR-013 §2.3/§2.5, и дренаж waiter'ов, нужный ADR-006 §2.5.

## 2. Решение

- **`InstanceHandle.Cancel(ctx)`** запрашивает завершение и блокируется
  (ограничено ctx) до терминального состояния экземпляра. **Экземпляр получает
  собственную отмену**: `Run` выводит `inst.ctx, inst.cancel = context.WithCancel(ctx)`
  и предоставляет `Cancel()`; ручка вызывает его, затем ждёт на существующем
  `Done()`/`loopDone`. Завершение по-прежнему исполняет проверенный каскад ADR-001
  (Active→Terminating→Terminated). `Suspend`/`Resume` **объявлены, но
  зарезервированы** (возвращают sentinel-ошибку) — им нужна отложенная подсистема
  `Paused` (ADR-013 §2.3).
- **`Thresher.Shutdown(ctx)`** грациозный, опосредованный движком: перевод в новое
  терминальное состояние **`Stopped`** (так что `StartProcess`/`RegisterProcess`/`Run`
  отвергают), отмена каждого работающего экземпляра и ожидание (ограничено ctx) их
  оседания, затем дренаж EventHub.
- **`EventHub.Shutdown(ctx)`** (реализует ADR-006 §2.5): остановить каждый waiter и
  **дождаться выхода их го-рутин** через хабовую `sync.WaitGroup`, ограничено ctx;
  удалить каждый waiter из реестра **даже если его `Stop()` вернул ошибку** (без
  утечки); разблокировать `Run` хаба.
- **Обнаружение + освобождение экземпляров.** Единая **`Instances(filter)`**
  (`InstancesAll` / `InstancesRunning` / `InstancesCompleted`) перечисляет id
  отслеживаемых экземпляров по живости (хост читает состояние каждого через
  `Instance(id)`); **`Thresher.Forget(ids ...string)`** освобождает **терминальные**
  экземпляры пакетно — всё-или-ничего (живой или неизвестный id — ошибка; при
  ошибке ничего не удаляется) — явное освобождение для удерживаемых-ради-наблюдения
  экземпляров. Поскольку у **event-start регистраций ещё нет экземпляра**, у них
  своё перечисление — **`Starters() []StarterInfo`** — read-only проекция
  зарегистрированных стартеров (процесс + триггер-событие + стартовый узел).
  **`UnregisterProcess`** сохраняет текущее поведение (удалить определение +
  стартеры; **живые экземпляры продолжают работать** на своём построенном снимке) —
  теперь задокументировано, с примечанием, что завершённые экземпляры освобождаются
  через `Forget`/`Shutdown`.

Управление грубое и видимое (операторское действие через машину состояний движка),
никогда не скрытый по-узловой listener (ADR-013 §4, ADR-011).

## 3. Функциональные требования

- **FR-1 — отмена экземпляра.** `InstanceHandle.Cancel(ctx context.Context)
  (InstanceState, error)` запрашивает завершение и блокируется, пока экземпляр не
  достигнет терминального состояния или `ctx` не завершится; возвращает терминальное
  состояние (+ `ctx.Err()` при таймауте). Идемпотентно (второй `Cancel` или `Cancel`
  уже-терминального экземпляра — no-op, возвращающий терминальное состояние).
- **FR-2 — хук самоотмены экземпляра.** `internal/instance`: `Run` выводит внутренний
  отменяемый контекст (`inst.cancel`); `Instance.Cancel()` отменяет его (идемпотентно),
  запуская существующий каскад `ctx.Done()` цикла к `Terminating`→`Terminated`.
  Родительский ctx Thresher'а (`instanceReg.stop`, ctx движка) по-прежнему каскадирует.
- **FR-3 — зарезервированные suspend/resume.** `InstanceHandle.Suspend(ctx)` /
  `Resume(ctx)` существуют и возвращают стабильный sentinel **`ErrNotImplemented`**
  (зарезервированы под подсистему `Paused`; контракт зафиксирован уже сейчас,
  ADR-013 §2.3).
- **FR-4 — грациозное завершение движка.** `Thresher.Shutdown(ctx context.Context)
  error`: (a) переход в `Stopped` (так что дальнейшие `StartProcess`/`RegisterProcess`/`Run`
  отвергаются); (b) отмена каждого работающего экземпляра и ожидание (ограничено ctx) их
  оседания; (c) вызов `EventHub.Shutdown(ctx)`; вернуть первую ошибку / `ctx.Err()` при
  дедлайне. Идемпотентно.
- **FR-5 — состояние `Stopped`.** Добавить `Stopped` в перечисление `State` Thresher'а;
  `StartProcess` (`thresher.go:623`), `RegisterProcess` и `Run` отвергают при `Stopped`.
- **FR-6 — синхронизированное завершение EventHub.** `EventHub.Shutdown(ctx context.Context)
  error` останавливает каждый зарегистрированный waiter, **ждёт выхода их `Service`-го-рутин**
  через хабовую `sync.WaitGroup`, ограничено `ctx`, и удаляет каждый waiter из реестра
  **даже когда `Stop()` возвращает ошибку** (залогировано, никогда не утекает).
  Разблокирует `Run` хаба и отвергает дальнейшую регистрацию. Го-рутина `Service`
  waiter'а сигналит хабу о выходе (она уже держит ссылку `hub` и вызывает `WaiterFired`).
- **FR-7 — забыть завершённые экземпляры (пакетно).** `Thresher.Forget(ids ...string)
  error` удаляет перечисленные **терминальные** экземпляры из карты `instances`,
  **всё-или-ничего**: сначала валидирует каждый id (известен **и** терминален) и не
  удаляет ничего, если хоть один неизвестен или ещё живой, возвращая ошибку с именем
  проблемного id. `Forget("x")` (одиночный) и `Forget(Instances(InstancesCompleted)...)`
  (зачистка) оба работают.
- **FR-7a — обнаружение экземпляров.** Единая `Thresher.Instances(filter InstanceFilter)
  []string` перечисляет id отслеживаемых экземпляров по перечислению `InstanceFilter`:
  `InstancesAll` (все отслеживаемые), `InstancesRunning` (нетерминальные — Created/Active/
  Terminating), `InstancesCompleted` (терминальные — Completed/Terminated; список, питающий
  пакетный `Forget`). Read-only, snapshot-консистентно под мьютексом движка.
- **FR-7b — обнаружение стартеров.** `Thresher.Starters() []StarterInfo` перечисляет
  зарегистрированные **event-start** регистрации (карта `starters`) — каждая это процесс,
  ожидающий событие, у которого **ещё нет экземпляра**, так что они не могут появиться в
  `Instances`. `StarterInfo` — read-only проекция внутреннего `instanceStarter`: процесс,
  который он инстанцирует, стартовый узел, срабатывающий при совпадении, и сообщение,
  которое он ждёт. Процесс с **ручным стартом** не регистрирует стартер, так что любой
  перечисленный стартер — авто-старт (поле `Manual` не нужно).
- **FR-8 — снятие регистрации при живых экземплярах.** `UnregisterProcess`
  (`thresher.go:450`) удаляет определение + стартеры и **оставляет любые живые экземпляры
  работающими** (текущее поведение, теперь задокументировано); per-process запись
  `snapshots` освобождается.

## 4. Нефункциональные требования

- **NFR-1 — грубое, опосредованное движком управление.** Cancel/Shutdown действуют через
  машины состояний экземпляра/движка (каскад ctx-cancel, `setState`), никогда не через
  чёрный ход; нет мутирующего по-узлового listener'а (ADR-013 §4).
- **NFR-2 — нет утечки го-рутин при завершении.** После возврата `EventHub.Shutdown`
  (в пределах `ctx`) ни одна `Service`-го-рутина waiter'а не переживает хаб; неудачный
  `Stop()` всё равно удаляет waiter (NFR реализует ADR-006 §2.5).
- **NFR-3 — ограниченность + идемпотентность.** `Cancel`/`Shutdown` уважают дедлайны
  `ctx` и идемпотентны (безопасно вызывать дважды / после терминала); сообщают об
  отставших, а не виснут.
- **NFR-4 — безопасность по конкурентности.** Мутации карт (`instances`/`snapshots`/`waiters`)
  остаются под их существующими мьютексами; каскад отмены остаётся моделью
  единоличного владения циклом (ADR-001); нет новой блокировки на горячем пути
  наблюдения/исполнения.
- **NFR-5 — покрытие.** Затронутые файлы финишируют ≥80% (цель 100%) diff-coverage;
  `make ci` зелёный (особенно `-race` — завершение конкурентно-нагружено).

## 5. Анализ путей (альтернативы)

- **Cancel через самоотмену экземпляра (выбрано) vs ручка держит `instanceReg.stop`.**
  Выбрано: экземпляр владеет своей отменой (`inst.cancel`, выведено в `Run`), `Cancel()`
  её триггерит. Ручка (которая держит `*instance.Instance`) вызывает `inst.Cancel()` —
  без обращения к thresher'у, завершение локально для ручки, и родительский каскад ctx
  движка не тронут. Отклонено «ручка держит thresher-отмену»: связывает ручку с
  внутренностями thresher'а и требует поиска.
- **`Cancel(ctx)` запрос-и-ожидание (выбрано) vs fire-and-forget.** Выбрано: запрос +
  ограниченное ctx ожидание терминала с возвратом состояния — симметрично с
  `WaitCompletion`, даёт вызывающему подтверждение. Fire-and-forget вынудил бы
  отдельный `WaitCompletion`.
- **Дренаж EventHub через хабовую `WaitGroup` + сигнал выхода го-рутины waiter'а
  (выбрано) vs синхронный `Stop()` (блокировать до выхода го-рутины).** Выбрано: хаб
  делает `Add` при старте waiter'а, а го-рутина waiter'а делает `Done` при выходе
  (зеркалит существующий колбэк waiter→hub `WaiterFired`); `Shutdown` делает `Stop`
  всем, затем `Wait`, ограничено ctx. Отклонён синхронный-`Stop`: меняет семантику
  `Stop()` каждого waiter'а и сериализует дренаж.
- **Удержание экземпляров: keep + `Forget` (выбрано) vs авто-вытеснение при завершении
  vs хранить вечно.** Выбрано (продуктовое решение): завершённые экземпляры остаются
  доступными для поиска (`Instance(id)`/`Observe`/`State` работают после завершения —
  живая in-memory модель ADR-013), с явным `Forget(id)` + `Shutdown` для освобождения.
  Отклонено авто-вытеснение: теряет наблюдение после завершения; отклонено хранить-вечно:
  неограниченно без освобождения.
- **`Stopped` как новое терминальное состояние (выбрано) vs переиспользовать `NotStarted`.**
  Выбрано: отдельное терминальное `Stopped`, чтобы остановленный движок не приняли за
  перезапускаемый свежий.
- **Обнаружение: одна `Instances(filter)` (выбрано) vs три метода
  (`Instances`/`RunningInstances`/`CompletedInstances`).** Выбрано: единая функция с
  именованным перечислением `InstanceFilter` (`InstancesAll`/`InstancesRunning`/
  `InstancesCompleted`) — 3-путевой фильтр, не булев, так что одна функция читается чище
  трёх и расширяется, если категории живости вырастут. Стартеры перечисляются
  **отдельно** через `Starters() []StarterInfo`, а не как четвёртое значение
  `InstanceFilter`, потому что стартер — **не экземпляр** (нет id экземпляра, нет
  состояния жизненного цикла); вложить его в `Instances()` означало бы возвращать id,
  которые не резолвятся через `Instance(id)`.
- **`Forget(ids ...string)` пакетно, всё-или-ничего (выбрано) vs одиночный-id / частично.**
  Выбрано: вариативный пакет (одиночный id тоже работает) с validate-all-then-remove, так
  что плохой id в зачистке оставляет карту нетронутой, а ошибка действенна.
- **`UnregisterProcess` допускает живые экземпляры (выбрано) vs отвергать / отменять их.**
  Выбрано (продуктовое решение): удалить определение + стартеры, оставить живые экземпляры
  работающими на построенном снимке; проще всего, без связывания unregister с завершением.
  Хост использует `Shutdown`/`Cancel`/`Forget` для жизненного цикла экземпляра.

## 6. API (публичная поверхность, `pkg/thresher`)

```go
// On the existing read-only handle (SRD-018), control is added:
func (h *InstanceHandle) Cancel(ctx context.Context) (InstanceState, error)
func (h *InstanceHandle) Suspend(ctx context.Context) error // reserved -> ErrNotImplemented
func (h *InstanceHandle) Resume(ctx context.Context) error  // reserved -> ErrNotImplemented

// Engine lifecycle:
func (t *Thresher) Shutdown(ctx context.Context) error

// Instance discovery + release:
type InstanceFilter uint8
const (
	InstancesAll       InstanceFilter = iota // every tracked instance
	InstancesRunning                          // non-terminal (Created/Active/Terminating)
	InstancesCompleted                        // terminal (Completed/Terminated)
)
func (t *Thresher) Instances(filter InstanceFilter) []string
func (t *Thresher) Forget(ids ...string) error // batch, terminal-only, all-or-nothing

// Event-start registrations (no instance yet). A manual-start process registers
// no starter, so a listed starter is always auto-start (hence no Manual field).
type StarterInfo struct {
	ProcessID string // the process a matching event instantiates
	StartNode string // the start node fired on a match
	Trigger   string // the message the starter waits on
}
func (t *Thresher) Starters() []StarterInfo

// New thresher state (terminal):
const Stopped State = /* iota after Paused */

// Reserved-feature sentinel:
var ErrNotImplemented = errs.New(/* "feature reserved, not yet implemented" */)
```

Внутренняя поддержка: `internal/instance` — `Run` выводит `inst.cancel`;
`Instance.Cancel()` (идемпотентно). `internal/eventproc/eventhub` —
`EventHub.Shutdown(ctx) error` + хабовая `sync.WaitGroup`; waiter'ы сигналят
о выходе `Service`-го-рутины хабу (например, колбэк `hub.waiterDone()` в паре с
`wg.Add(1)` на `w.Service(eh.ctx)` в `registerWaiter`).

## 7. План тестирования

- **`TestInstanceHandleCancel`** — `Cancel(ctx)` на припаркованном/долгоиграющем
  экземпляре доводит его до `Terminated`; второй `Cancel` и `Cancel` завершённого
  экземпляра — no-op'ы, возвращающие терминальное состояние (FR-1, FR-2).
- **`TestCancelCtxBounded`** — `Cancel` с коротким ctx против экземпляра, который не
  осядет, возвращает `ctx.Err()` и нетерминальное состояние (FR-1).
- **`TestSuspendResumeReserved`** — оба возвращают `ErrNotImplemented` (FR-3).
- **`TestThresherShutdown`** — `Shutdown(ctx)` отменяет работающие экземпляры,
  переводит в `Stopped` и закрывает хаб; `StartProcess`/`RegisterProcess`/`Run` затем
  отвергают; идемпотентно при втором вызове (FR-4, FR-5).
- **`TestShutdownDrainsWaiters`** (`-race`) — с зарегистрированным таймер-waiter'ом
  `EventHub.Shutdown` возвращается только после выхода го-рутины waiter'а (без утечки);
  waiter, чей `Stop()` ошибается, всё равно удаляется (FR-6, NFR-2).
- **`TestForget`** — `Forget(ids...)` удаляет завершённые экземпляры пакетно
  (последующий `Instance(id)` → false); работает по принципу **всё-или-ничего** — пакет,
  содержащий живой или неизвестный id, не удаляет ничего и ошибается с его именем (FR-7).
- **`TestInstancesFilter`** — `Instances(InstancesAll/InstancesRunning/InstancesCompleted)`
  возвращает правильные наборы id по ходу прогона (припаркованный экземпляр под Running,
  завершённый под Completed, оба под All);
  `Forget(Instances(InstancesCompleted)...)` зачищает завершённые (FR-7a).
- **`TestStarters`** — процесс, зарегистрированный с message-start событием, появляется в
  `Starters()` со своим id процесса + триггером; режим ручного старта отражён; процесс без
  event-start не имеет записи стартера (FR-7b).
- **`TestUnregisterProcessWithLiveInstance`** — `UnregisterProcess` успешен при живом
  работающем экземпляре; определение исчезло, но экземпляр завершается (FR-8).
- Внутренние юниты `internal/eventproc/eventhub` + `internal/instance` для
  `EventHub.Shutdown` / `Instance.Cancel` (атрибуция межпакетного покрытия).

## 8. Кросс-документная консистентность

- **Реализует** [ADR-013 v.1](../design/ADR-013-instance-observability.ru.md) §2.3
  (грубое управление), §2.5 (жизненный цикл движка: `Shutdown`/`UnregisterProcess`).
- **Реализует** [ADR-006 v.1 §2.5](../design/ADR-006-events-and-subscriptions.ru.md) —
  синхронизированную через `WaitGroup` остановку waiter'ов + no-leak-on-`Stop` (половина
  с единоличным владением уже приземлена через FIX-003).
- [ADR-001 v.5](../design/ADR-001-execution-model.ru.md) — общий каскад ctx-отмены, который
  триггерит `Cancel` (Active→Terminating→Terminated).
- [ADR-002 v.2](../design/ADR-002-extension-architecture.ru.md) — поверхность публичного API
  §4.7, к которой присоединяются эти методы.
- [SRD-018 v.1](SRD-018-instance-observe-handle.ru.md) — срез наблюдения, на который этот
  строит поверхность управления (близнец).
- Ссылки вверх/вбок, с пином версии; нет ссылок вниз (ADR-013/ADR-006 не цитируют SRD-019).

## 9. Definition of Done

- FR-1…FR-8 подключены и проверены тестами §7 (включая `-race` тест дренажа waiter'ов).
- `InstanceHandle.Cancel`/`Suspend`/`Resume`, `Thresher.Shutdown`/`Forget`, состояние
  `Stopped` + охранники, и `EventHub.Shutdown` + хабовая `WaitGroup` — все присутствуют.
- Ни одна го-рутина waiter'а не переживает `EventHub.Shutdown` (NFR-2) — доказано под `-race`.
- `make ci` зелёный (tidy, lint incl. fieldalignment, build, `-race`, diff-coverage ≥95,
  govulncheck); затронутые файлы ≥80% (цель 100%).
- Все 9 примеров smoke-run выходят с 0 (управление/завершение не сломали happy path).
- §10 заполнена; статус → Принято; добавлен RU-близнец; связанные доки синхронизированы.

## 10. Сводка реализации

Приземлено на `feat/instance-control-lifecycle` (от `master`): четыре вехи, каждая —
один коммит, плюс смежное улучшение примера.

### 10.1 Вехи (коммиты)

| M | Коммит | Объём | Тесты |
|---|---|---|---|
| M1 | `a35012a` | Отмена экземпляра: `inst.cancel` выведен в `Run`, `Instance.Cancel()`; `InstanceHandle.Cancel(ctx)` (запрос + ограниченное ctx ожидание, идемпотентно); зарезервированные `Suspend`/`Resume` → `ErrNotImplemented` | `TestInstanceHandleCancel`, `TestCancelCtxBounded`, `TestSuspendResumeReserved`, `TestInstanceCancel` (внутренний) |
| M2 | `0c1ffab` | Дренаж waiter'ов `EventHub.Shutdown(ctx)`: `EventWaiter.Done()` + `Shutdown` хаба; таймер/message waiter'ы закрывают канал `done` при выходе го-рутины; `started bool` заменён перечислением `hubState` | `TestEventHubShutdownDrainsWaiters` (-race), `TestShutdownRemovesOnStopError`, `TestShutdownCtxBounded`; проверки `Done()` waiter'а в тестах таймера/message |
| M3 | `75dcde6` | `Thresher.Shutdown(ctx)` + терминальное состояние `Stopped`; `Run` выводит `t.engineCancel` (главный рубильник каскада); охранники в `Run`/`StartProcess`/`RegisterProcess` | `TestThresherShutdown`, `TestThresherShutdownCtxBounded` |
| M4 | `f3ee4a3` | Обнаружение + освобождение: `Instances(filter)`+`InstanceFilter`, `Forget(ids...)` (пакетно всё-или-ничего), `Starters()`+`StarterInfo`; примечание к `UnregisterProcess` | `TestInstancesFilter`, `TestForget`, `TestStarters`, `TestUnregisterProcessWithLiveInstance` |

Плюс `ba32f4e` — базовый пример читает property + `RUNTIME/STARTED_AT` через
`DataReader` gofunc'а (разбит по правилу >80 строк для примеров); смежно к этому
SRD, в фазе примеров этой ветки.

### 10.2 Ключевые файлы

- `pkg/thresher/handle.go` — `Cancel`/`Suspend`/`Resume` + `ErrNotImplemented`.
- `pkg/thresher/thresher.go` — состояние `Stopped`, `engineCancel`, `Shutdown`, охранники.
- `pkg/thresher/discovery.go` (новый) — `InstanceFilter`, `Instances`, `Forget`, `StarterInfo`, `Starters`.
- `internal/instance/instance.go` — `inst.cancel` + `Instance.Cancel()`.
- `internal/eventproc/eventproc.go` — `EventWaiter.Done()` + `EventHub.Shutdown` на интерфейсах.
- `internal/eventproc/eventhub/eventhub.go` — перечисление `hubState`, `EventHub.Shutdown`, охранник регистрации.
- `internal/eventproc/eventhub/waiters/{timer,message}.go` — канал `done` + `Done()`.

### 10.3 Верификация

- `make ci` зелёный: tidy, lint (incl. fieldalignment), build, `-race`,
  **diff-coverage 96.8% из 189 изменённых строк (≥95)**, govulncheck.
- Покрытие затронутых функций всё ≥80% (большинство 100%; `Shutdown` 96%, `Run` 87%,
  `RegisterProcess` 96%).
- Все 9 примеров smoke-run выходят с 0.
- NFR-2 (ни одна го-рутина waiter'а не переживает хаб) доказано под `-race`.

### 10.4 Дельты против черновика

- **Поля `StarterInfo`.** Черновик сначала набросал `{ProcessID, Trigger, Manual}`;
  отгружено как `{ProcessID, StartNode, Trigger}` — процесс с **ручным стартом не
  регистрирует стартер**, так что перечисленный стартер всегда авто-старт (поля `Manual`
  нет). §6/FR-7b согласованы в `f3ee4a3`.
- **Имена тестов дренажа waiter'ов.** Единственный `TestShutdownDrainsWaiters` из §7
  приземлился как `TestEventHubShutdownDrainsWaiters` (дренаж реального таймера под
  `-race`) + `TestShutdownRemovesOnStopError` (половина «Stop ошибся → всё равно удалён») +
  `TestShutdownCtxBounded`; то же покрытие, разбито для ясности.
- **Жизненный цикл EventHub.** M2 заменил `started bool` перечислением `hubState`
  (`notStarted`/`started`/`stopped`), чтобы флаг завершения не мог образовать недопустимую
  комбинацию «started-и-stopped» — небольшое уточнение сверх черновика.

## История документа

| Версия | Дата | Автор | Изменение |
|---|---|---|---|
| v.1 | 2026-06-18 | Руслан Габитов | Принято. Приземляет срез управления + жизненного цикла движка из ADR-013 v.1 (§2.3/§2.5) и реализует открытую часть ADR-006 v.1 §2.5: `InstanceHandle.Cancel(ctx)` (самоотмена экземпляра через выведенный в `Run` `inst.cancel`, запрос + ограниченное ctx ожидание, идемпотентно) + зарезервированные `Suspend`/`Resume`; `Thresher.Shutdown(ctx)` (новое терминальное состояние `Stopped`, отмена + оседание работающих экземпляров, дренаж хаба); `EventHub.Shutdown(ctx)` (хабовая `sync.WaitGroup` над `Service`-го-рутинами waiter'ов, ограниченное ctx ожидание, remove-even-on-`Stop`-error); `Thresher.Forget(ids ...string)` (пакетное, всё-или-ничего освобождение терминальных экземпляров) + обнаружение `Instances(filter)` (одна функция, `InstancesAll`/`InstancesRunning`/`InstancesCompleted`) + `Starters() []StarterInfo` (event-start регистрации, у которых ещё нет экземпляра) + `UnregisterProcess` задокументирован (живые экземпляры продолжают работать). Обосновано по коду `pkg/thresher`, `internal/instance`, `internal/eventproc/eventhub`. Близнец SRD-018 v.1 (наблюдение). Реализует ADR-013 v.1; реализует ADR-006 v.1 §2.5; ссылается на ADR-001 v.5, ADR-002 v.2. |
