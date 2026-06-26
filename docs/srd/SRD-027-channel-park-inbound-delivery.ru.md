# SRD-027 — Доставка входящих событий с парковкой на канале (входящий срез ADR-017)

| Поле | Значение |
|---|---|
| Статус | Принято |
| Версия | v.1 |
| Дата | 2026-06-25 |
| Владелец | Ruslan Gabitov |
| Реализует | [ADR-017 v.1 Channel-based event processing](../design/ADR-017-channel-based-event-processing.md) §2 Rule 1 (входящий срез) |

Этот SRD приземляет **входящий срез** ADR-017: ждущий track перестаёт делать
busy-spin и **паркуется на канале своего track'а**; производители событий
перестают мутировать track на чужой goroutine и вместо этого **эмитят сработавшее
событие в loop конкретного instance**, который является **единственным
отправителем** в канал track'а. Обращённый к хабу `EventProcessor` —
**на-каждый-триггер** (ADR-017 §2 Rule 1): **Instance** для Message (корреляция
владеется instance'ом, поэтому уточняющий `validateAndAssociate` выполняется в
loop'е), **track** для Signal/Timer. Это убирает busy-spin `runtime.Gosched` и
per-track `eventMu`, делает отложенный выбор атомарным в loop'е и заменяет O(n)
сканирование при signal-broadcast'е индексом по имени. **Исходящий срез** (loop,
владеющий позициями токенов — ADR-017 Rule 2) — **это SRD-028, не здесь**.

---

## 1. Контекст и текущее состояние (проверено по коду)

ADR-001 v.5 делает per-instance **loop** единственным писателем состояния
жизненного цикла: track'и `emit`-ят `trackEvent`-ы на `inst.events`, а `loop()`
применяет их на одной goroutine (`internal/instance/instance.go:608,619`).
Доставка событий обходит эту дисциплину на входящей стороне:

- **Синхронная доставка на чужой goroutine.** `EventProducer` дотягивается до
  track'а через `track.ProcessEvent` (`internal/instance/track.go:870`), который
  **мутирует track на goroutine производителя** — читает `t.steps`, прогоняет
  узел, продвигает плечо и переключает состояние в `TrackReady`. Signal — худший
  случай: `PropagateEvent → broadcastSignal → w.Process → ProcessEvent`
  выполняется целиком на goroutine'е того, кто бросил событие
  (`internal/eventproc/eventhub/eventhub.go:419,498`).
- **Регистрация track'а протекает состоянием instance'а.** Сегодня track — это
  обращённый к хабу `EventProcessor`: `checkNodeType` вызывает
  `t.instance.RegisterEvent(t, d)` для **каждого** триггера (`track.go:371`), а
  `track.CorrelationKeys()` (`track.go:325-331`) выставляет **instance'а** conversation
  keys для keyed-message-подписки — состояние instance'а всплывает через track
  лишь потому, что регистрируется именно track.
- **Busy-spin-ожидание.** Ждущий track не блокируется — `track.run` крутится на
  `TrackWaitForEvent` с `runtime.Gosched()` (`internal/instance/track.go:444-456`),
  жжёт CPU и требует явного yield'а, чтобы не заморить loop голодом.
- **Два мьютекса, замазывающие две goroutine.** `eventMu` (`track.go:180-187`)
  сериализует конкурентный `ProcessEvent`, чтобы два срабатывания плеч на
  Event-Based-шлюзе не смогли оба пройти guard `TrackWaitForEvent` (FIX-007); `m`
  защищает `t.steps`, потому что его трогают и goroutine производителя, и goroutine
  прогона.
- **O(n) signal-broadcast.** `broadcastSignal` линейно сканирует **всех** waiter'ов
  и матчит по имени (`eventhub.go:465-514`); реестр — это
  `map[eDef.ID()]EventWaiter` (`eventhub.go:50`).

ADR-017 (Draft) решил структурный фикс. Этот SRD реализует его **входящую**
половину.

## 2. Требования

### Функциональные

- **FR-1 — per-track канал парковки.** `track` выставляет буферизованный канал
  `evtCh` и, находясь в `TrackWaitForEvent`, **блокируется** в `select` на нём
  (ноль CPU) вместо кручения. `evtCh` имеет фиксированный буфер на один слот
  (§3.6).
- **FR-2 — производители эмитят, никогда не мутируют; зарегистрированный
  процессор — на-каждый-триггер.** `eventproc.EventProcessor.ProcessEvent`
  производителя больше не мутирует track — он **эмитит** `evDeliver` `trackEvent`
  в loop и возвращается. Процессор, который держит хаб, выбирается по триггеру:
  **track** для Signal/Timer (`track.ProcessEvent` эмитит `evDeliver{track, eDef}`),
  **Instance** для Message (FR-8). Ни в одном из случаев состояние track'а не
  читается и не пишется на goroutine'е производителя.
- **FR-3 — loop — единственный отправитель в `evtCh`.** Только `loop()` отправляет
  в `evtCh` track'а (диспетчеризация) и только `loop()` закрывает его (демонтаж).
  Ни один производитель не держит ссылку на канал track'а.
- **FR-4 — отложенный выбор атомарен в loop'е.** Loop держит loop-локальное
  множество припаркованных-и-недоставленных track'ов. Track, входящий в ожидание,
  эмитит `evWaiting` (что добавляет его); **первый** подходящий `evDeliver` для
  этого track'а удаляет его (переключение) и отправляет событие; каждый
  последующий `evDeliver` для него находит его отсутствующим и **отбрасывается**
  (проигравшее плечо Event-Based-шлюза / дубликат срабатывания). Это держится и
  для **смешанно-триггерного** шлюза (плечо-сообщение через Instance, плечо-таймер
  через track): оба приземляются в один и тот же loop, нацеливаясь на один и тот
  же track. Двойной выигрыш при конкурентном срабатывании из FIX-007 произойти не
  может, а `eventMu` убран.
- **FR-5 — порядок park-before-register и засев индекса.** Track эмитит `evWaiting`
  **до** того, как регистрирует свои waiter'ы в хабе, так что — поскольку
  `inst.events` FIFO, а регистрация happens-before любого подходящего `evDeliver`
  — loop всегда записывает track как припаркованный прежде, чем событие сможет в
  него нацелиться. `evWaiting` несёт ID **message**-catch-определений track'а,
  которые loop в той же точке также заносит в индекс `msgEDef.ID() → track` из FR-8
  (а `spawn` засевает оба для track'ов, которые стартуют припаркованными при
  конструировании). Ни одно сработавшее событие не теряется на ещё-не-известный-как-припаркованный
  track.
- **FR-6 — signal-broadcast использует индекс по имени.** Хаб поддерживает
  `signalName → []subscriber`, строящийся при register/unregister; `broadcastSignal`
  ищет имя и вызывает `ProcessEvent` каждого track'а-подписчика (→ emit в loop
  этого track'а), заменяя O(n) сканирование всех waiter'ов. Broadcast без catcher'а
  даёт пустую выборку и потому является безвредным no-op'ом (ADR-006 v.1 §2.4).
  Cross-instance fan-out сохранён (signal не ограничен областью в пределах
  досягаемости).
- **FR-7 — Stop будит припаркованный track.** `loop()`/`stopAll`, закрывая `evtCh`
  track'а, будит track, заблокированный в `select` из FR-1 (приём на закрытом
  канале), который затем отменяется — существующий флаг `stopIt` покрывает только
  выполняющийся путь.
- **FR-8 — Message регистрируется на гранулярности instance'а (гибридная
  граница).** Для **Message**-catch'а зарегистрированный `eventproc.EventProcessor`
  — это **Instance**, а не track (Signal/Timer остаются track-зарегистрированными —
  FR-2). `Instance.ProcessEvent` эмитит `evDeliver{eDef}` (несущий **никакого**
  track'а) в loop; loop разрешает целевой track через per-instance индекс
  `msgEDef.ID() → track` (FR-5) и прогоняет уточняющий гейт корреляции
  (`validateAndAssociate`) **перед** переключением — несовпадение **отбрасывает**
  событие и **оставляет track припаркованным** (получатель продолжает ждать). При
  совпадении loop переключает и диспетчеризирует. `CorrelationKeys()` переезжает с
  `track` на `Instance` (keyed-подписка у брокера владеется instance'ом).
  Обоснование: ADR-017 v.1 §2 Rule 1 + Engine note — корреляция — единственное
  matching-состояние с областью instance'а, и в BPMN она только-Message
  (Correlation §8.4.2).

### Нефункциональные

- **NFR-1 — никакой новой гонки.** `go test -race ./...` чист; per-track `eventMu`
  убран, потому что состояние track'а на приёме трогает только его собственная
  goroutine (доставка — единственный потребитель).
- **NFR-2 — семантика Message не изменилась; гейт переезжает в loop.** Two-tier
  match корреляции сохранён в том, *что* он матчит: брокер делает грубый match по
  имени+ключу (ADR-014/016), а `validateAndAssociate` (`instance.go:375`,
  под `convMu`) делает уточняющий match — теперь прогоняемый в **`loop()`**, когда
  Instance эмитит входящее сообщение (FR-8), а не на goroutine'е track'а.
  Несовпадение оставляет track припаркованным (он продолжает ждать), никогда его не
  продвигает; вердикт идентичен, меняется только решающая goroutine.
- **NFR-3 — флейки отложенного-выбора / abort'а вылечены (настоящая первопричина).**
  Убрать busy-spin (FR-1) было необходимо, но **недостаточно**: complex/OR-join
  `-race`-флейки (`TestComplexRequiredGate`, `TestComplexAbortOnDeath`,
  `TestComplexAbortInstance`) имели две различные гонки по времени в loop'овой
  перепроверке join'а — двойное чтение позиций токенов и abort, не останавливавший
  loop детерминированно (§3.8). Оба исправлены; `pkg/thresher` под `-race`
  проходит 40/40 (было ~1/6).
- **NFR-4 — diff-покрытие ≥ COVER_MIN (95%) на затронутых функциях**, с прицелом
  на 100%.

## 3. Модели

### 3.1 `track` — канал парковки, убрать мьютекс события (`internal/instance/track.go`)

Добавить `evtCh chan flow.EventDefinition` (ёмкость = константа `eventBufferDepth`
из §3.6 — один слот), создаётся при конструировании track'а. **Убрать**
`eventMu sync.Mutex` (`track.go:180-187`) — доставка теперь единственным
потребителем на собственной goroutine'е track'а, так что сериализовать нечего.

Ветка `TrackWaitForEvent` в `run()` (`track.go:444-456`) заменяет spin
`runtime.Gosched` блокирующей парковкой и на приёме прогоняет тело доставки на
**этой** goroutine'е:

```go
case TrackWaitForEvent (in run's loop):
    select {
    case <-ctx.Done():
        t.updateState(TrackCanceled); t.lastErr = ctx.Err(); return
    case eDef, ok := <-t.evtCh:
        if !ok {            // loop closed it on stop (FR-7)
            t.updateState(TrackCanceled); return
        }
        if err := t.deliver(ctx, eDef); err != nil {
            t.updateState(TrackFailed); t.lastErr = err; return
        }
    }
```

`deliver` — это тело, поднятое из сегодняшнего `ProcessEvent`, теперь **без** шага
корреляции (loop уже его прогейтил — FR-8) и **без** guard'а
`eventMu`/`!inState` для чужой goroutine (loop гарантирует единственную доставку
припаркованному track'у): `ProcessEvent` узла → `unregisterEvent` →
`advanceToArm` → `updateState(TrackReady)`.

### 3.2 Входные точки производителя — неблокирующие emit'ы (`internal/instance`)

**Signal/Timer — `track.ProcessEvent` (`track.go:870`):**

```go
// ProcessEvent (eventproc.EventProcessor) is called by a Signal/Timer producer goroutine. It no
// longer touches track state — it hands the event to the loop, which dispatches it to t.evtCh.
func (t *track) ProcessEvent(_ context.Context, eDef flow.EventDefinition) error {
    t.instance.emit(trackEvent{kind: evDeliver, track: t, eDef: eDef})
    return nil
}
```

**Message — `Instance.ProcessEvent` (новый; `internal/instance/instance.go`):**

```go
// ProcessEvent (eventproc.EventProcessor) is the hub-facing entry for Message: the Instance is the
// registered processor (FR-8), because message correlation state is instance-owned. It emits the
// event to its own loop carrying NO track — the loop resolves the parked track via the msgEDef→track
// index and runs validateAndAssociate before dispatch.
func (inst *Instance) ProcessEvent(_ context.Context, eDef flow.EventDefinition) error {
    inst.emit(trackEvent{kind: evDeliver, eDef: eDef}) // track == nil ⇒ the message branch (§3.4)
    return nil
}

// CorrelationKeys moves here from track (track.go:325): the keyed broker subscription is
// instance-owned, so the Instance is what the message waiter type-asserts for its keys.
func (inst *Instance) CorrelationKeys() []string { /* the instance's conversation key values */ }
```

`Instance` тем самым удовлетворяет `eventproc.EventProcessor`
(`foundation.Identifyer` + `ProcessEvent`); instance-starter
(`pkg/thresher/instance_starter.go`) уже устанавливает паттерн процессора на
гранулярности instance'а (ADR-015).

### 3.3 `trackEvent` — поле `eDef`, виды `evDeliver` + `evWaiting` (`internal/instance/event.go`)

`trackEvent.eDef flow.EventDefinition` (`event.go:8`) несёт сработавшее
определение; `evWaiting` несёт ID message-catch-определений track'а (FR-5). Два
вида (`event.go:52`) и их плечи `String()`:

```go
// evWaiting: the track entered TrackWaitForEvent (emitted BEFORE it registers its hub waiters,
// FR-5). The loop records it as parked-and-undelivered and indexes its message defs → track.
evWaiting
// evDeliver: a producer handed a fired event to the loop (FR-2). A track-carried evDeliver
// (Signal/Timer) targets ev.track directly; a track-less one (Message via Instance.ProcessEvent,
// FR-8) is resolved through the msgEDef→track index and correlation-gated. The loop dispatches to
// the track's evtCh iff it is parked-and-undelivered, else drops it (FR-4).
evDeliver
```

### 3.4 `loop()` — множество припаркованных, индекс сообщений, гейтированная диспетчеризация, демонтаж (`internal/instance/instance.go:619`)

Две loop-локальные карты, только-для-loop-goroutine (без блокировки — как
`active`/`stopping`):

```go
waiting := map[string]struct{}{}        // track.ID() ⟺ parked-and-undelivered
msgIdx  := map[string]*track{}          // waited message eDef.ID() → parked track (FR-5/FR-8)
```

Новые плечи `switch`:

```go
case evWaiting:
    waiting[ev.track.ID()] = struct{}{}
    for _, id := range ev.msgDefIDs {       // message catch defs this track parks on
        msgIdx[id] = ev.track
    }

case evDeliver:
    tr := ev.track                          // Signal/Timer carry the track …
    if tr == nil {                          // … Message is resolved via the index (FR-8)
        tr = msgIdx[ev.eDef.ID()]
        if tr == nil {                       // no parked track for this message → drop
            break
        }
    }
    if _, parked := waiting[tr.ID()]; !parked {
        break                                // losing arm / already-delivered → drop (FR-4)
    }
    if ev.track == nil && inst.validateAndAssociate(ctx, ev.eDef) {
        break                                // correlation mismatch → drop, KEEP parked (FR-8/NFR-2)
    }
    flipNotParked(tr, waiting, msgIdx)       // remove from waiting + clear tr's msgIdx entries
    tr.evtCh <- ev.eDef                      // sole sender; buffered single slot
```

`flipNotParked` удаляет `waiting[tr.ID()]` и каждую запись `msgIdx`, указывающую на
`tr` (так что позднее событие на проигравшее плечо-сообщение того же track'а
отбрасывается). `stopAll` закрывает `evtCh` каждого живого track'а (FR-7) —
безопасно, потому что loop — единственный отправитель — и очищает обе карты.
`evEnded`/`evFailed`/`evAwaiting` также выключают track из множества, так что
запись множества/индекса никогда не переживает свой track. `spawn` засевает
`waiting` + `msgIdx` для любого track'а, который стартует припаркованным при
конструировании (до того как loop сдренирует `inst.events`, где emit `evWaiting`
привёл бы к deadlock'у — FR-5).

### 3.5 Индекс имён сигналов (`internal/eventproc/eventhub/eventhub.go`)

Добавить `signalIdx map[string][]eventproc.EventWaiter`, поддерживаемый в
`registerWaiter`/`UnregisterEvent` (`eventhub.go:198,331`) для waiter'ов, чьё
определение — `TriggerSignal`. `broadcastSignal` (`eventhub.go:465`) ищет по
имени вместо сканирования `eh.waiters`; для каждого подписчика он маршрутизирует
событие в loop instance'а track'а (через существующий путь `track.ProcessEvent →
emit` из §3.2 — signal остаётся **track**-зарегистрированным). Поведенческого
изменения в том, какие catcher'ы получают, нет — только стоимость поиска и
goroutine доставки.

### 3.6 Фиксированная глубина буфера — константа, не опция (`internal/instance`)

```go
// eventBufferDepth is the per-track inbound event-channel capacity. One slot is exactly
// enough: the loop dispatches at most one event per parked episode (it flips the track out
// of the waiting set on first delivery, §3.4), and a single slot decouples the loop's
// send from the track's scheduling so the loop never blocks. Unbuffered would risk
// blocking the loop in the window between evWaiting and the track reaching its receive.
const eventBufferDepth = 1
```

Ни опции движка, ни поля `thresherConfig`: у глубины ровно одно правильное значение
при flip-on-dispatch, так что ручка была бы настройкой, которую никто не должен
крутить. Если в будущем появится нужда (например, отложенная работа по
durability/replay, ADR-017 §5) — ввести опцию тогда.

### 3.7 Регистрация на-каждый-триггер в `checkNodeType` (`internal/instance/track.go:335`)

Цикл по-определениям выбирает процессор по типу триггера — единственное место, где
выбирается гибридная граница:

```go
for _, d := range defs {
    proc := eventproc.EventProcessor(t)        // Signal/Timer: the track is the processor
    if d.Type() == flow.TriggerMessage {
        proc = t.instance                       // Message: the Instance owns correlation (FR-8)
    }
    if err := t.instance.RegisterEvent(proc, d); err != nil {
        return /* … wrapped … */
    }
}
```

`evWaiting` (эмитится прямо над этим циклом, FR-5) несёт ID `TriggerMessage`-определений
в `defs`, так что `msgIdx` loop'а разрешает сработавшее сообщение обратно в этот
track. `track.CorrelationKeys` удалён (keyed был только message-путь, и он теперь
принадлежит Instance'у — §3.2).

### 3.8 Перепроверка join'а без гонок (`internal/instance/reachability.go`, `instance.go`)

Убирание busy-spin'а позволило loop'овой перепроверке reachability/activation-join
(SRD-022 / SRD-023) выполняться в моменты, которых она раньше никогда не достигала,
вскрыв две предсуществовавшие гонки по времени, из-за которых complex/OR-join-тесты
флейчили под `-race`:

- **Двойное чтение позиций токенов.** `recheckJoin` сэмплировал живые позиции
  **дважды** — один раз для in-transit-guard'а (старый `hasInTransitArrival`),
  затем снова для reachability (`Recheck` → `CheckFlows` → `occupiedNodes`). Токен,
  соскальзывающий с узла-ветки (где он делает свой join-поток *достижимым*) на
  узел join'а (*arrived-pending*, его входящий поток ещё не помечен), читался как
  «на ветке» guard'ом (→ продолжить), но «на join'е» reachability (→ недостижим),
  так что обязательный поток выглядел **ни пришедшим, ни достижимым** → ложный
  abort *«complex gateway activation rule is unsatisfiable»* (и симметричный
  *пропущенный* abort). **Фикс:** один снимок. `joinPositions(node)` делает
  **единственный** проход по `inst.tracks`, возвращая `(occupied, inTransit)` —
  каждая позиция читается ровно один раз; `recheckJoin` откладывает на `inTransit`
  и передаёт `Recheck`-у `fixedFlowChecker`, привязанный к тому же множеству
  `occupied`, так что guard и reachability никогда не смогут разойтись между двумя
  чтениями. `hasInTransitArrival` убран; `occupiedNodes` делегирует в
  `joinPositions(nil)`.
- **Abort не останавливал loop детерминированно.** Abort activation-join'а вызывал
  `inst.fail`, который лишь записывает `lastErr` и отменяет ctx instance'а,
  полагаясь на то, что loop *затем* выберет `<-ctx.Done()` → `stopAll`. Но отмена
  также будит припаркованные track'и, чьи события `evEnded` гоняются с плечом
  `<-done`; если они первыми сдренировали `active` до 0 (Go `select` выбирает
  случайно среди готовых плеч), loop выходил со `stopping == false` → instance
  сообщал **Completed** вместо **Terminated**. **Фикс:** путь abort'а
  (и guard-ошибки) вызывает `stopAll()` сразу после `inst.fail()` — в соответствии
  с существующим паттерном `failFromTrack`; `stopAll` протянут через
  `recheckParked` / `recheckAwaitingJoins` / `recheckJoin`.

## 4. Анализ

- **Путь (выбран) — ADR-017 Rule 1 (Model Y, loop-диспетчеризация, граница
  на-каждый-триггер).** Производители эмитят в loop; loop — единственный
  отправитель. Граница хаба — Instance для Message и track иначе. Обоснование,
  решение гибрид-vs-единообразный (Alternative E) и отклонённые альтернативы
  (прямой per-track канал; per-site блокировки; одна грубая блокировка) живут в
  **ADR-017 v.1 §2 + §4** — здесь не повторяются.
- **Почему Instance только для Message.** Корреляция — единственная причина поднять
  границу хаба с track'а, и она — Message-only-условие в BPMN (Correlation §8.4.2).
  Сделать Instance процессором сообщений ставит conversation keys, keyed-подписку и
  гейт `validateAndAssociate` к одному владельцу и удаляет утечку
  `track.CorrelationKeys`. Signal/Timer сохраняют более простую track-регистрацию —
  маршрутизация их через Instance вынудила бы внутренний re-fan-out для broadcast'а
  без какой-либо подходящей выгоды (ADR-017 §4 E).
- **Почему гейт переезжает в loop.** Когда `track.ProcessEvent` сведён к emit'у,
  уточняющий match не может остаться синхронным return'ом на goroutine'е
  производителя. Loop — уже единственный писатель instance'а и владелец состояния
  под `convMu` — естественное место его прогона; несовпадение — это loop-локальный
  drop, оставляющий track припаркованным, без хопа re-park'инга track'а.
- **Почему `ProcessEvent` сохраняет сигнатуру.** И track, и Instance реализуют
  существующий `eventproc.EventProcessor`; вызывающие сайты хаба/waiter'а не
  изменены. Только *тело* съезжает с чужой goroutine, а *зарегистрированный объект*
  различается по триггеру. Наименьший радиус поражения.
- **Почему loop-локальные карты, а не состояние track'а, читаемое loop'ом.** Чтение
  состояния track'а из loop'а — это cross-goroutine-чтение, запрещаемое Rule 2
  (предмет SRD-028). `waiting` и `msgIdx` владеются loop'ом единолично, так что
  Slice 1 не вводит нового cross-read'а.
- **Почему park-before-register (FR-5).** Это единственная опасность порядка:
  событие, срабатывающее в окне между subscribe и park. Эмит `evWaiting` (с ID
  определений сообщений) до `RegisterEvent` кладёт запись о парковке и запись
  индекса на `inst.events` впереди любого `evDeliver`, который может вызвать
  регистрация (FIFO), закрывая окно без блокировки.
- **Engine notes — drop отложенного выбора корректен, не теряет.** Отбрасывание
  `evDeliver` для отсутствующего (уже-доставленного / не-припаркованного) track'а
  всегда отбрасывает лишь проигравшее плечо Event-Based-шлюза или дубликат
  срабатывания уже-потреблённого catch'а — никогда триггер, который припаркованный
  track всё ещё ждёт. Drop по несовпадению корреляции (FR-8) — отдельный случай: он
  оставляет track припаркованным.
- **Вне области (явно).** Loop, владеющий позициями токенов / состоянием join'а
  (ADR-017 Rule 2, исходящее) → **SRD-028**. Никакого изменения в том, *что* матчит
  корреляция сообщений (ADR-014/016), в планировании таймеров или в разрешении плеч
  Event-Based-шлюза (`eventRouter`/`advanceToArm`).
- **Синхронная связка производитель→loop (известна, ограничена).** Производители
  вызывают `ProcessEvent` (track'а или Instance'а) синхронно, а `emit` — это
  `select { events<-ev; <-loopDone }` — так что вызов ограничен временем жизни
  instance'а: ни deadlock'а, ни send-on-closed, устаревшая ссылка на процессор —
  no-op. Единственная остаточная стоимость — head-of-line-задержка signal-broadcast'а
  (последовательный fan-out на goroutine'е бросившего, ограниченный скоростью
  дренажа loop'а); message — per-waiter-goroutine и не затронут. Полное
  асинхронное расцепление — это отложенный буферизованный intake (ниже / ADR-017
  v.1 §3, §5), при измеренной конкуренции — не этот срез.
- **`inst.events` остаётся небуферизованным (вне области).** Этот срез добавляет
  `evDeliver`/`evWaiting` на существующий per-instance intake, но **не**
  перенастраивает его ёмкость. `inst.events` небуферизован по контракту backpressure
  единственного писателя из ADR-001 (`emit` блокируется на `select { events<-ev;
  <-loopDone }`), а loop никогда не блокируется при дренаже — flip-on-dispatch
  отправляет только в припаркованный-и-недоставленный track, так что `evtCh <- eDef`
  всегда приземляется в свободный единственный слот. Конфигурируемый буфер
  loop-intake'а — это ручка пропускной способности ядра-loop'а, не концерн доставки
  событий; вводить его только при измеренной конкуренции, отдельным изменением.

## 5. Поверхность публичного API

- **Никакого нового публичного API.** Глубина буфера — неэкспортируемая константа
  (§3.6), не опция. Регистрация на-каждый-триггер и индекс сообщений — внутренние
  (`internal/instance`).
- **Изменён реализатор (тот же интерфейс):** `Instance` теперь реализует
  `eventproc.EventProcessor` (`ProcessEvent` + `CorrelationKeys`) для Message-пути;
  `track` сохраняет `ProcessEvent` для Signal/Timer и **теряет** `CorrelationKeys`.
  Хосты, реализующие кастомные `EventProcessor`-ы, не видят изменения сигнатуры.
- **Изменена семантика (та же сигнатура):** `eventproc.EventProcessor.ProcessEvent`
  возвращается, как только событие **поставлено в очередь instance-loop'а**, а не
  как только оно применено. Доставка и упорядочивание (per-track FIFO через канал)
  становятся частью документированного асинхронного контракта (ADR-017 §5).

## 6. Тестовые сценарии

- **T-1 (FR-1, NFR-3):** catch-event-процесс завершается на сработавшем событии
  **без busy-spin** — проверяется через отсутствие пути `runtime.Gosched` (ждущая
  goroutine заблокирована) и стабильное время.
- **T-2 (FR-4, FIX-007):** два плеча Event-Based-шлюза срабатывают конкурентно →
  ровно одно плечо продвигается, другое отбрасывается; `-race` чист, повторяется N×
  без двойного выигрыша.
- **T-3 (FR-5):** событие, сработавшее в окне subscribe→park, всё равно доставлено
  (park-before-register) — не потеряно.
- **T-4 (FR-6):** broadcast-signal достигает каждого catcher'а в досягаемости через
  несколько instance'ов по индексу имён; broadcast без catcher'а — no-op (без
  ошибки, debug-лог).
- **T-5 (FR-7):** остановка/отмена instance'а с припаркованным track'ом завершается
  оперативно (закрытый `evtCh` будит его) — без зависания.
- **T-6 (FR-8, NFR-2):** сообщение, чья корреляция не совпадает, оставляет
  получателя ждущим (оставлен припаркованным **loop**-гейтом — `Instance.ProcessEvent`
  → `evDeliver{eDef}` → drop в loop'е), совпадающее — продвигает его; гейт
  выполняется в `loop()`, не на goroutine'е track'а.
- **T-7 (регрессия):** `make ci` зелёный; `TestComplexAbortInstance` и прежние
  poll-wait-флейки проходят под `-race`.
- **T-8 (FR-2/FR-8, гибрид):** Message-catch регистрирует **Instance** как процессор
  хаба, а Signal-catch регистрирует **track**; **смешанно-триггерный**
  Event-Based-шлюз (плечо-сообщение + плечо-таймер) разрешает обе доставки на одном
  loop'е/track'е и выбирает ровно одного победителя.
- **T-9 (NFR-3, §3.8 перепроверка join'а):** complex/OR-join `-race`-стресс
  стабилен — `pkg/thresher` под `-race` проходит 40/40 (было ~1/6);
  `TestComplexRequiredGate` больше не делает ложный abort, а
  `TestComplexAbortOnDeath` достигает **Terminated**, не **Completed**.

## 8. Cross-doc

- **Реализует** [ADR-017 v.1](../design/ADR-017-channel-based-event-processing.md) §2 Rule 1 — вверх, с версией.
- **Сохраняет** [ADR-006 v.1](../design/ADR-006-events-and-subscriptions.md) §2.4 (no-catcher no-op),
  [ADR-014 v.1](../design/ADR-014-message-handling.md) / [ADR-016 v.1](../design/ADR-016-message-correlation.md)
  (two-tier-корреляция — *что* она матчит, не изменилось; уточняющий шаг переезжает в loop, а
  Message-процессор становится Instance'ом).
- **Замещает механизм** FIX-007 (`eventMu` + post-guard re-read) — входящий срез
  убирает причину существования guard'а; FIX-007 остаётся замороженным историческим
  record'ом (не редактируется).
- **Чинит перепроверку** [SRD-022](SRD-022-inclusive-or-join.md) (OR-join) /
  [SRD-023](SRD-023-complex-gateway.md) (complex-шлюз) — §3.8 делает их
  loop-сайд-перепроверку без гонок; *что* эти join'ы решают, не изменилось, убраны
  только гоночные чтения. Вбок (SRD→SRD); без version-пина — SRD/FIX single-shot.
- **Парный срез:** SRD-028 (исходящее, loop-владеемые позиции) — второй срез
  ADR-017; **не** в этом change-set'е.

## 9. Definition of Done

- [x] FR-1…FR-8 подключены и задействованы тестами §6.
- [x] `eventMu` и busy-spin `runtime.Gosched` убраны; `track.CorrelationKeys`
      переехал в `Instance`; новый мьютекс не добавлен.
- [x] `make ci` зелёный (tidy → lint → build → `-race`-тесты → diff-покрытие ≥ 95%
      на затронутых функциях → govulncheck), по модулям.
- [x] Запускаемые примеры smoke-прогон (не только build) выходят с 0 (дисциплина
      FIX-002).
- [x] §8 cross-doc-пины консистентны (только вверх/вбок; каждый ссылаемый док
      существует на своём пине).
- [x] §10 сводка по реализации заполнена (файлы/строки, V-результаты, SHA
      майлстоунов). ADR-017 приземляется как Accepted теперь, когда SRD-028
      (исходящий срез) тоже приземлился (двухсрезовый раскат концепции завершён).

## 10. Сводка по реализации

Приземлено на `feat/adr-017-eps-rework`.

### 10.1 Коммиты

| Stage | SHA | Scope |
|-------|-----|-------|
| M1 — входящая обвязка + диспетчеризация loop'а | `ce44831` | `evDeliver` trackEvent + путь диспетчеризации loop'а в припаркованный track; добавлен `t.evtCh`; emit производитель→loop заменяет cross-goroutine `track.ProcessEvent`. |
| M2 — доставка входящих с парковкой на канале | `f9ee4c6` | ждущий track паркуется на `t.evtCh` (busy-spin убран); `eventMu` убран; строители waiter'ов (`message.go`/`waiters.go`) перестают мутировать track; loop-владеемый демонтаж подписки. |
| M3 — гибридный instance-как-процессор (Message) | `ba1648c` | **Instance** — это хаб-`EventProcessor` для Message (корреляция владеется instance'ом); `validateAndAssociate` выполняется в loop'е; целевой track разрешается через per-instance индекс `msgEDef → track`; Signal/Timer остаются track-адресуемыми. |
| M4 — O(1) индекс имён сигналов | `1ef7609` | индекс `signalName → subscribers` в eventhub'е заменяет O(n) сканирование всех waiter'ов в `broadcastSignal`. |
| §3.8 — перепроверка complex/OR-join без гонок | `4c8828f` | single-snapshot перепроверка/abort по позициям join'а (intra-MR-пластырь для переходного cross-read'а loop'а — позже **замещён** loop-владеемым представлением `position` из SRD-028). |

### 10.2 Ключевые файлы

- `internal/instance/event.go` — виды `evDeliver`/`evWaiting`; `trackEvent` несёт `eDef`/`msgDefIDs`.
- `internal/instance/instance.go` — диспетчеризация loop'а, множество `waiting`, индекс `msgEDef → track`, `validateAndAssociate` в loop'е, `Instance.ProcessEvent`/`Instance.CorrelationKeys`.
- `internal/instance/track.go` — `t.evtCh`, парковка-на-канале в `run`, `eventMu`/`runtime.Gosched` убраны.
- `internal/eventproc/eventhub/{eventhub,waiters/message,waiters/waiters}.go` — индекс имён сигналов, loop-управляемый демонтаж waiter'а.

### 10.3 Верификация

- **§6 тесты.** `inbound_delivery_test.go`, `conversation_key_test.go`, `eventhub_signal_test.go`, `message_test.go` — зелёные под `-race`.
- **`make ci`.** Зелёный по всем модулям (tidy, lint 0 issues, build, `-race`-тесты, diff-покрытие, govulncheck).
- **Diff-покрытие.** `covercheck -min 95 -base origin/master` PASS — eventhub.go / waiters/message.go / waiters/waiters.go / event.go / instance.go 100%, track.go 98.7% (прогон ветки, общий с SRD-028).
- **`-race`-стресс.** `pkg/thresher` под `-race` ×40 чист.
- **Примеры.** Все 16 `examples/*` прогоняются до exit 0 (runtime-smoke-дисциплина FIX-002).

### 10.4 Дельты относительно черновика

- Добавлена **§3.8** (перепроверка complex/OR-join без гонок) как intra-MR-правка, когда переход loop'а M2/M3 вскрыл переходный cross-read на join'е; single-snapshot-перепроверка — это пластырь входящего среза, **замещённый** loop-владеемым представлением `position` из SRD-028 (cross-read'а нет вовсе).
- В остальном приземление соответствует черновику: FR-1…FR-8 подключены как специфицировано; ни scope не добавлен, ни убран.

## Открытые вопросы

Нет.
