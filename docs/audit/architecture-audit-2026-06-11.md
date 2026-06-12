# Архитектурный аудит GoBPM

**Дата:** 2026-06-11
**Метод:** мультиагентный анализ (6 параллельных обзоров: ядро thresher/runner, событийная подсистема, instance/snapshot/scope, слой BPMN-модели, границы пакетов и API, сверка ADR/SRD с кодом) с ручной верификацией критических находок по исходникам.

---

## TL;DR

Архитектурный замысел — сильный: ADR-дисциплина образцовая (Accepted ADR-001/002/009 реализованы точно по спецификации, roadmap честно отражает состояние), модель «track как горутина, token как проекция» — правильное решение. Но между замыслом и кодом есть три системных разрыва:

1. **Конкурентность** в thresher/eventhub/instance написана с TOCTOU-разрывами и неработающими no-op методами — это не стиль, это баги.
2. **Слой модели заражён знанием о runtime** (`pkg/model` импортирует `internal/exec`), что убивает заявленную чистоту слоёв.
3. **Публичный API библиотеки фактически write-only** — пользователь может запустить процесс, но не может узнать, чем он закончился.

---

## 1. Подтверждённые баги

Эти находки проверены вручную по исходному коду, не только агентами.

### 1.1 `launchInstance` отменяет контекст инстанса сразу после запуска — CRITICAL

`pkg/thresher/thresher.go:398`

Код создаёт `ctx, cancel := context.WithCancel(t.ctx)`, делает `defer cancel()`, запускает асинхронный `inst.Run(ctx)` и **сохраняет тот же `cancel` как `stop`** в реестре инстансов:

```go
ctx, cancel := context.WithCancel(t.ctx)
defer cancel()
if err := inst.Run(ctx); err != nil { ... }

t.instances[inst.ID()] = instanceReg{
    stop: cancel,   // cancel уже будет вызван через defer
    inst: inst,
}
```

Логическое противоречие в чистом виде: либо `defer cancel()` убивает инстанс через мгновение после старта, либо отмена игнорируется — и тогда сохранённый `stop` ничего не останавливает. Оба варианта неверны. **Самый срочный фикс.**

### 1.2 Гонка с потерей данных в `addData` — CRITICAL

`internal/instance/instance.go:457-485`

Map скоупа читается под `RLock`, затем **мутируется без блокировки** (если скоуп существовал, `vv` — живая map; конкурентная запись из двух треков даёт `fatal: concurrent map writes`), затем перезаписывается под `Lock`:

```go
inst.m.RLock()
vv, ok := inst.scopes[path]
inst.m.RUnlock()          // <-- блокировка отпущена

for _, d := range dd {
    vv[dn] = d            // <-- мутация живой map без lock
}

inst.m.Lock()
inst.scopes[path] = vv    // <-- может затереть чужие обновления
inst.m.Unlock()
```

При параллельных треках (а это сердце BPMN) — потеря записей или паника рантайма. Фикс: держать `Lock` на всю операцию.

### 1.3 Двойной `close(stopCh)` в timer waiter — CRITICAL

`internal/eventproc/eventhub/waiters/timer.go:260` и `timer.go:350`

`runTimerService` при `ctx.Done()` делает `close(tw.stopCh)`; `Stop()` тоже делает `close(tw.stopCh)`. При гонке — `panic: close of closed channel`. Фикс: `sync.Once` либо закрывать только в одном месте.

Там же — отладочный `fmt.Println("stopping waiter ...")` в production-коде (`timer.go:271`).

### 1.4 No-op методы, которые тихо врут — MAJOR

- `Instance.UnregisterEvent` — `internal/instance/instance.go:618-623` — возвращает `nil`, ничего не делая.
- `timeWaiter.RemoveEventProcessor` — `internal/eventproc/eventhub/waiters/timer.go:199-201` — то же.

Следствие: процессоры никогда не отписываются, waiters никогда не доходят до состояния «пусто → остановиться», горутины и подписки текут. Это хуже отсутствия метода — вызывающий код строит логику на ложном успехе (`eventhub.UnregisterEvent` реально проверяет `len(w.EventProcessors()) == 0` после «удаления»). Фикс: реализовать либо возвращать `ErrUnsupported`.

### 1.5 TOCTOU в `EventHub.RegisterEvent` — MAJOR

`internal/eventproc/eventhub/eventhub.go:117-146`

Паттерн `RLock → проверка → RUnlock → CreateWaiter → Lock → запись`: две горутины с одним `eDef.ID()` создадут два waiter'а, второй молча затрёт первый вместе с его горутиной. Фикс: одна блокировка на всю операцию (double-checked под `Lock`).

### 1.6 Мелкие, но реальные

| Находка | Локация | Суть |
|---|---|---|
| `Array.GetKeys()` | `pkg/model/data/values/array.go:192-202` | `make([]any, len)` + `append` → слайс двойной длины с nil-половиной; нужно `res[i] = i` |
| `RemoveParameter` value receiver | `pkg/model/data/io_spec.go:248` | мутирует копию, изменения не видны вызывающему; нужен pointer receiver |
| Мёртвые состояния трека | `internal/instance/track.go:74,81` | `TrackProcessStepResults`, `TrackMerged` объявлены, но недостижимы |
| `tokenStateFor` не покрывает `TrackCreated` | `internal/instance/token.go:78-95` | падает в `default → TokenInvalid` |
| Несогласованные ошибки в waiters | `waiters/waiters.go:20-26` | `fmt.Errorf` вместо `errs.New` |

---

## 2. Системные архитектурные проблемы

### 2.1 Слоистость нарушена в самом дорогом месте — CRITICAL

`pkg/model/activities/user_task.go`, `service_task.go`, `pkg/model/events/event.go` импортируют `internal/exec`, `internal/interactor`, `internal/renv` — модель BPMN реализует executor-интерфейсы рантайма.

Следствия:

- модель нельзя использовать отдельно (валидация, визуализация, экспорт процесса без исполнения);
- пользователь не может написать собственный тип задачи — сигнатура `Exec(ctx, renv.RuntimeEnvironment)` требует internal-тип.

ADR-003 §4.4 предписывает depguard для контроля направлений импортов — **его в `.golangci.yml` нет**, поэтому нарушение и прошло.

**Лечение:** visitor/registry-паттерн для executor'ов (модель описывает структуру, исполнители регистрируются отдельно) + depguard в CI.

### 2.2 Публичный API write-only — MAJOR

Через `Thresher` можно зарегистрировать и запустить процесс — и всё. `Instance`, `Token`, состояние, история, подписка на завершение — всё в `internal/`. В `examples/basic-process` это видно буквально: запустили, напечатали «started», узнать исход невозможно.

Смежные дыры жизненного цикла движка:

- нет `Thresher.Shutdown(ctx)` — graceful-останов невозможен;
- `snapshots` map растёт без `UnregisterProcess` — утечка памяти (`pkg/thresher/thresher.go:351-353`).

**Лечение:** публичный `InstanceHandle` (`State()`, `Tokens()`, `WaitCompletion(ctx)`), `Shutdown`, `UnregisterProcess`.

### 2.3 Instance — god object — MAJOR

`internal/instance/instance.go` (852 строки) — один тип реализует `eventproc.EventProducer` + `renv.RuntimeEnvironment` + `scope.Scope` + lifecycle + токены. Каждая роль тянет свои инварианты блокировок — гонка в `addData` (п. 1.2) и есть прямое следствие того, что scope-логика живёт внутри объекта с чужой дисциплиной локов.

**Лечение:** выделить `InstanceScope` в отдельный тип — это и рефакторинг, и фикс гонки.

### 2.4 Семантика доставки событий не определена — MAJOR

Фактически **at-most-once**: `PropagateEvent` без зарегистрированного waiter'а — ошибка (`eventhub.go:222-242`), буфера нет; событие, опубликованное до подписки, теряется. Потребители же (по комментариям и логике треков) ожидают гарантированную доставку.

**Лечение:** решить в ADR-006 явно — буферизация pending-событий либо жёсткий контракт «подписка строго до публикации».

### 2.5 Незакрытые жизненные циклы waiters — MAJOR

Кто владеет waiter'ом — hub или сам waiter — решено в коде двумя способами одновременно: waiter удаляет себя в `processTimerEvent` (`timer.go:319`), hub удаляет его в `UnregisterEvent` (`eventhub.go:255`). Нет синхронизации завершения горутин waiter'ов при shutdown (нет WaitGroup), при ошибке `Stop()` waiter остаётся в map с работающей горутиной.

### 2.6 Хрупкая дисциплина мьютекса в Thresher — MAJOR

`pkg/thresher/thresher.go:369-375` — корректность держится на комментарии («release BEFORE launchInstance ... re-acquire t.m», FIX-002 RC2). Любой рефакторинг без чтения комментария вернёт self-deadlock. Рассмотреть atomic для state и явное разделение «методы под локом / без лока».

---

## 3. Точки сложности

### 3.1 `pkg/model/data` — самое тяжёлое место

- `io_spec.go` (414) + `io_spec_obj.go` (471) = **885 строк на одну концепцию** с двусторонними связями IoSpec ↔ Parameter ↔ Set и размазанной валидацией.
- `values/array.go` (449 строк) — Array со встроенной callback-системой нотификаций; напрашивается разделение «структура» / «нотификации» (decorator).

### 3.2 Event options — 8 интерфейсов-адаптеров

`pkg/model/events/event_options.go:18-99` — `cancelAdder`, `timerAdder`, `messageAdder`… с runtime type assertions: ошибки несовместимости триггера и типа события всплывают в рантайме, а не при компиляции. Философия options у activities и events противоположная (builder со сборкой IoSpec vs приём готовых `EventDefinition`) — нужно унифицировать.

### 3.3 Валидация и мутабельность модели процесса

- Валидация графа живёт только в `snapshot.New()`; `Process.Add/Remove` публичны и работают без проверок (можно добавить flow на несуществующие ноды).
- Process можно мутировать **после создания снапшота и во время исполнения** — `pkg/model/process/process.go:168-226`. Нужны `Process.Validate()` и заморозка после первого снапшота.
- Type assertions без проверки в `process.go:177-180` (`e.(flow.Node)`, `e.(*flow.SequenceFlow)`) — panic при некорректном `EType()`.

### 3.4 BPMN-покрытие (плановое, но влияет на оценку зрелости)

Реализован только Exclusive gateway; Parallel/Inclusive/Complex отсутствуют. `SubProcessActivity` объявлен как константа без реализации. `boundaryEvents` в Activity — поле есть, но нет ни `AttachBoundaryEvent`, ни options. `WithCompensation()` — голый флаг без привязки к компенсируемой activity.

---

## 4. Что НЕ является проблемой (задокументированные отсрочки)

Сверка с ADR/SRD подтвердила: следующее — **явно** зафиксированные отсрочки, не скрытые дефекты:

| Тема | Где задокументировано |
|---|---|
| Гонка `RegisterData` на shared node | ADR-001 §6 / ADR-005 §3 → Persistence & State ADR |
| Event-подписки без per-instance ключа (`eDef.ID()` only) | SRD-006 FR-7 → ADR-006 |
| Parallel/Inclusive/Complex gateways | roadmap WS-C1, ADR-005 (Draft) |
| Persistence & rehydration | roadmap WS-B3 |
| Runtime-оверлей (`runtime/` — stub) | ADR-004 (Draft), roadmap WS-D |
| Long-wait goroutine release | ADR-007 (Draft) |

Консистентность Accepted-ADR ↔ код близка к образцовой: ADR-001 v.5 (каналы, отсутствие token registry, token-как-проекция), ADR-002 (functional options, 9 контрактов расширений), ADR-009/SRD-006 (`Snapshot.Clone` — node-loop / flow-loop / default-flow remap) реализованы точно по тексту.

Единственная честная претензия к документации: **snapshot позиционируется около recovery, но `tracks`, `scopes` и история в него не сохраняются** — он только template для запуска свежих инстансов. Это стоит написать прямо, пока нет Persistence ADR. Туда же: `Snapshot.Clone()` шарит `Properties` (`snapshot.go:106` — копируется только заголовок слайса указателей) — пока Properties неизменяемы это безопасно, но инвариант нигде не закреплён ни тестом, ни доком.

Два MAJOR-расхождения с ADR, требующих действия (а не ожидания):

1. **ADR-003 §4.4 depguard + conformance helpers** — заявлены, в CI отсутствуют.
2. **`runtime/` и `adapters/sqlite/` без собственных `go.mod`** — ADR-003 требует отдельные модули; без них пользователи core получают runtime-зависимости транзитивно.

---

## 5. Приоритеты

### Сейчас (баги, чинятся точечно)

1. `launchInstance`: убрать `defer cancel()`, время жизни контекста = время жизни Instance (п. 1.1).
2. `addData`: полный `Lock` на всю операцию (п. 1.2).
3. Двойной `close(stopCh)` в timer waiter + убрать `fmt.Println` (п. 1.3).
4. Реализовать `UnregisterEvent` / `RemoveEventProcessor` либо возвращать ошибку (п. 1.4).
5. TOCTOU в `EventHub.RegisterEvent` (п. 1.5).
6. Мелочь из п. 1.6 (`GetKeys`, `RemoveParameter` receiver, мёртвые состояния).

### Следующий ADR/SRD-цикл

7. depguard в CI (ADR-003 §4.4) — поймал бы нарушение слоёв автоматически.
8. Разрыв `pkg/model → internal/exec` (visitor/registry для executor'ов).
9. Публичный `InstanceHandle` + `Thresher.Shutdown()` + `UnregisterProcess`.
10. `go.mod` для `runtime/` и `adapters/sqlite/`.

### Плановое

11. Декомпозиция Instance (начать с выделения Scope).
12. Владение жизненным циклом waiters (hub vs waiter — выбрать одно).
13. Семантика доставки событий — закрепить в ADR-006.
14. Ревизия слоя data (объединение io_spec, decorator для нотификаций Array).
15. `Process.Validate()` + заморозка модели после снапшота.

---

## Общая оценка

Проект в фазе «каркас правильный, мышцы сырые». Документационная дисциплина (ADR/SRD/roadmap с пиннингом версий и verification-гейтами) — лучшая из того, что встречается в проектах такого размера, и именно она позволяет чинить пункты раздела 1 точечно, не трогая замысел. Главный долгосрочный риск — не баги конкурентности (они локальны), а протечка runtime в слой модели: чем дольше она живёт, тем дороже visitor-рефакторинг.
