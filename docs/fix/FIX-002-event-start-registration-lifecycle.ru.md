# FIX-002 — Дедлок StartProcess: повторный (re-entrant) мьютекс движка + guard на Created для стартового события

> Перевод. Канонична английская версия: [FIX-002-event-start-registration-lifecycle.md](FIX-002-event-start-registration-lifecycle.md). При расхождении приоритет у английского текста.

| Поле | Значение |
|---|---|
| Статус | Принято |
| Версия | v.1 |
| Дата | 2026-06-09 |
| Владелец | Руслан Габитов |
| Связанные | [ADR-001 v.4 §4.2 Execution Model](../design/ADR-001-execution-model.md) (жизненный цикл); [ADR-006 v.1 Events & Subscriptions](../design/ADR-006-events-and-subscriptions.md); [FIX-001](FIX-001-thresher-eventhub-startup-race.md) (разделение EventHub на Start/Run) |

## 1. Симптомы

Каждый вызов `StartProcess` **уходит в дедлок** (`fatal error: all goroutines are asleep - deadlock!`). Это проявляют два примера — оба вызывают `engine.StartProcess`:

- **`examples/basic-process`** (простой Start → ServiceTask → End): дедлок, без полезного вывода.
- **`examples/timer-event`** (таймер **на стартовом событии**): раскрывается в три слоя.
  1. Сначала падает на *построении* (RC1) —

     ```
     Failed to start process: couldn't create an Instance for process "…"
       Error: couldn't register event definitions
         node_name: timer-start
         Error: instance isn't active (current state: Created)
     ```
  2. Как только этот guard на этапе построения ослаблен, происходит дедлок в `StartProcess` ровно как у basic-process (RC2).
  3. С исправленными RC1+RC2 `StartProcess` отрабатывает, таймер **корректно срабатывает на 5 с**, выполняется service task и инстанс завершается — после чего в дедлок уходит *сам пример* (RC3): паника дедлока приходит на **5.01 с** (замерено), в момент срабатывания таймера, когда последняя рабочая горутина движка завершается, оставляя только `main` примера и `eventHub.Run`, заблокированные на `context.Background().Done()` (nil-канал).

`examples/simple-timer` **не** вызывает `StartProcess` (только `RegisterProcess` + `Run`), поэтому не задевает ни один путь движка и завершается корректно — почему он и выглядел «рабочим».

Скрыто от CI, потому что CI только **собирает** модули примеров, но никогда их не **запускает** (`make build-all` не имеет шага запуска) — см. §5.

## 2. Анализ первопричин

Существуют **три** независимых дефекта. Два — в движке на пути `StartProcess` (RC1, RC2); третий — в примере (RC3) и проявился только после исправления RC1+RC2. Простой процесс задевает RC2; процесс со стартовым событием задевает сначала RC1, затем RC2, затем (для `timer-event`) RC3.

### RC1 — `RegisterEvent` отвергает регистрацию на этапе построения (`Created`)

Узлы со стартовым событием регистрируют свои определения во время `instance.New`, пока инстанс в состоянии `Created`, но `Instance.RegisterEvent` требует `Active`:

- `instance.go:161` — `New` оставляет состояние `Created`; `createTracks()` выполняется внутри `New` (`instance.go:172`).
- `track.go:271` / `track.go:281-293` — `newTrack` → `checkNodeType` регистрирует определения событийного узла: `t.instance.RegisterEvent(t, d)` («couldn't register event definitions»).
- `instance.go:570-576` — `is := inst.State(); if is != Active { return "instance isn't active (current state: Created)" }`.
- `instance.go:260` — инстанс становится `Active` только позже, в `Run`.

Guard «только `Active`» (добавленный вместе с согласованием `Created→Active` из ADR-001 v.4) слишком строг: он смешивает «не регистрировать на **завершённом** инстансе» (корректно) с «не регистрировать во время **построения**» (неверно — именно тогда и регистрируются узлы со стартовым событием).

### RC2 — повторный (re-entrant) `Thresher.m` приводит `StartProcess` к дедлоку (более глубокий баг)

`StartProcess` удерживает мьютекс движка **сквозь** `launchInstance`, который повторно захватывает тот же неперевходимый `sync.Mutex`:

- `thresher.go:368-378` — `StartProcess` делает `t.m.Lock(); defer t.m.Unlock()`, затем `return t.launchInstance(s)` — всё ещё удерживая `t.m`.
- `thresher.go:403` — `launchInstance` сам делает `t.m.Lock()` (реестр инстансов) → **повторный самодедлок**. Это `basic-process` (стартовое событие не нужно).
- Для процесса со стартовым событием дедлок проявляется раньше: с ослабленным RC1 `instance.New` → `RegisterEvent` → `Thresher.RegisterEvent` (`thresher.go:266`) → `Thresher.State()` (`thresher.go:184`, `t.m.Lock()`) → дедлок. Это `timer-event`.

Наблюдаемый стек дедлока (RC1 ослаблен): `main → StartProcess(:378, удерживает t.m) → launchInstance → instance.New → createTracks → newTrack → checkNodeType → Instance.RegisterEvent(:597) → Thresher.RegisterEvent(:266) → Thresher.State(:184) → sync.Mutex.Lock [заблокирован навсегда]`. `sync.Mutex` неперевходим; второй захват в той же горутине блокируется. `simple-timer` избегает этого только потому, что никогда не вызывает `StartProcess`.

### RC3 — `examples/timer-event` ждёт на nil-канале (баг примера, не движка)

С исправленными RC1+RC2 движок выполняет процесс корректно, но *сам пример* не может завершиться:

- `examples/timer-event/main.go:106` — `ctx := context.Background()` передаётся в `engine.Run(ctx)`.
- `examples/timer-event/main.go:123-127` — `main` затем блокируется на `select { case <-ctx.Done(): … }`. `context.Background().Done()` возвращает `nil`, поэтому это ожидание на nil-канале навсегда (баннер «Press Ctrl+C to exit» подразумевает обработчик сигнала, который пример никогда не устанавливает).
- `eventhub.go:93` — `EventHub.Run` так же блокируется на `<-ctx.Done()` (тот же `Background` ctx, nil-канал) на всё время жизни хаба — это нормально, это просто ожидание остановки.

Таким образом, после того как таймер сработал и инстанс завершился, остаются только две живые горутины — `main` и `eventHub.Run`, обе припаркованы на nil-канале; рантайм Go фиксирует «all goroutines are asleep» и паникует.

**Движок доказанно корректен — дефекта доставки таймера на стороне движка нет.** Доказательства:

- Паника дедлока приходит на **5.01 с** (`time` замерено на собранном бинарнике) — ровно `timeDate` таймера (`time.Now().Add(5*time.Second)`, `main.go:37/39`). Таймер сработал, одноразовый waiter выполнил `processTimerEvent` и завершился (`timer.go:248` `go runTimerService` → срабатывание → `RemoveWaiter`), поэтому в дампе горутин его нет — не потому, что он не стартовал.
- Замена ожидания в примере на отменяемый `context.WithTimeout(…, 8s)` приводит к тому, что программа печатает `Process completed` и завершается с кодом 0, без изменений движка.

`examples/basic-process` не имеет RC3 — он возвращается из `main` после `StartProcess`, а не паркуется на `ctx.Done()`, поэтому уже отрабатывает до конца после исправления RC2.

## 3. Решение

RC1 и RC2 — исправления движка; RC3 — исправление примера. Все три нужны, чтобы оба сломанных примера заработали.

### 3.1 — RC1: разрешить регистрацию в нетерминальных состояниях

`Instance.RegisterEvent` отвергает только **терминальный** инстанс; разрешает `Created`/`Active`. Порядок enum: `Created(0) < Active(1) < Completed(2) < Terminating(3) < Terminated(4)` (`instance.go:56-67`).

```go
// internal/instance/instance.go — RegisterEvent
is := inst.State()
if is != Created && is != Active {
    return errs.New(
        errs.M("instance is terminal, can't register events (state: %s)", is),
        errs.C(errorClass, errs.InvalidState),
        errs.D("requester_id", proc.ID()))
}
```

Регистрация законна на построении (стартовые события) и в рантайме (граничные / промежуточные перехватывающие события, `Active`); отказывать нужно только когда инстанс уже не может реагировать на сработавшее событие (терминальное состояние). EventHub уже запущен `Thresher.Run` (FIX-001), поэтому хаб его принимает; блокировал только этот guard.

### 3.2 — RC2: `StartProcess` не должен удерживать `t.m` сквозь `launchInstance`

Сузить блокировку движка до поиска снапшота; освободить её до `launchInstance` (который сам берёт `t.m` для реестра):

```go
// pkg/thresher/thresher.go — StartProcess
func (t *Thresher) StartProcess(processID string) error {
    if t.State() != Started { return /* not started */ }

    t.m.Lock()
    s, ok := t.snapshots[processID]
    t.m.Unlock()
    if !ok { return /* not found */ }

    return t.launchInstance(s) // launchInstance locks t.m on its own
}
```

Это убирает **оба** пути повторного входа: прямой re-lock `launchInstance:403` (basic-process) и re-lock `State()` во время регистрации стартового события (timer-event). Также заменить незащищённое чтение `t.state` в `thresher.go:361` на `t.State()`.

### 3.3 — RC3: дать `examples/timer-event` завершаемое ожидание

Передать `engine.Run` отменяемый контекст и ждать на нём (чтобы программа корректно завершилась после срабатывания таймера), вместо парковки `main` на `context.Background().Done()`:

```go
// examples/timer-event/main.go
ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
defer cancel()
err = engine.Run(ctx)
// …
<-ctx.Done() // bounded; the 5 s timer fires well within the window
fmt.Println("Process completed")
```

Бюджет в 8 с даёт 5-секундному таймеру `timeDate` время сработать, а инстансу — завершиться до отмены контекста. Использование простого `<-ctx.Done()` (а не `select` с одним case) также снимает линт `gosimple S1000`. Код движка не тронут.

### 3.4 Рассмотренные альтернативы

- **Сделать `Thresher.State()` без блокировки (atomic-состояние), как у `Instance`.** Убирает повторный вход через `State()` (timer-event), но НЕ прямой re-lock `launchInstance:403` (basic-process) — сама по себе недостаточна. Разумное *дополнительное* усиление; опционально, можно сделать позже.
- **Рекурсивный мьютекс.** Отклонено — неидиоматично для Go; повторная блокировка маскирует проблемы дизайна.
- **Отложить регистрацию стартового события до первого выполнения трека (после `Active`).** Отклонено для RC1 — более инвазивно и ортогонально RC2 (который всё равно бы дедлочил basic-process).

## 4. Верификация

| # | Проверка | Ожидание |
|---|---|---|
| V1 | Юнит: инстанс в `Created` регистрирует определение стартового события без ошибки (RC1). | успех на `Created`. |
| V2 | Юнит: `RegisterEvent` на терминальном (`Completed`/`Terminated`) инстансе по-прежнему возвращает ошибку (RC1). | guard сохранён. |
| V3 | Юнит/интеграция: `StartProcess` для простого процесса **и** для процесса со стартовым событием возвращается без дедлока (RC2). | нет повторной блокировки; `t.m` не удерживается сквозь `launchInstance`. |
| V4 | `examples/basic-process` отрабатывает до конца (RC2). | код 0; нет дедлока, нет «instance isn't active». |
| V5 | `examples/timer-event` отрабатывает до конца (движок RC1+RC2, пример RC3): таймер срабатывает на ~5 с, инстанс завершается, программа выходит. | код 0; печатает `Process completed`; нет паники дедлока. |
| V6 | Без регрессий: `make ci` зелёный (race-тесты + diff-coverage + vuln); `examples/simple-timer` по-прежнему работает. | все существующие тесты проходят. |

## 5. Предотвращение

- **CI запускает примеры, а не только собирает их.** Весь этот класс отказов скрылся из-за того, что `make build-all` никогда не выполняет модули примеров. Добавить шаг CI (и `make`-таргет), который запускает каждый пример с таймаутом и проверяет код выхода 0. Заведено как follow-up; см. [[project_examples_runtime_broken]].

## 6. Регрессии / заметки по объёму

- **`examples/basic-process` в объёме** — это та же повторная блокировка RC2 (ранее ошибочно списанная на пустой `ServiceTask`); исправляется §3.1 + §3.2.
- **`examples/timer-event` требует всех трёх** — исправления движка (§3.1 + §3.2) плюс исправление примера (§3.3). Гипотеза RC4 «движок никогда не планирует таймер» была **исследована и опровергнута**: таймер срабатывает ровно на 5.01 с, так что единственный оставшийся дефект — ожидание примера на nil-канале.
- Изменение guard (§3.1) не должно ослаблять защиту терминального состояния (V2).
- Сужение блокировки `StartProcess` (§3.2) должно сохранить защиту мьютексом и чтения снапшота, и записи в реестр — просто не удерживать её сквозь `launchInstance`. Конкурентные вызовы `StartProcess` остаются безопасными.
- RC3 (§3.3) — изменение только примера; поведение движка не меняется.

## 7. Связанное

- [ADR-001 v.4 §4.2](../design/ADR-001-execution-model.md) — жизненный цикл (`Created → Active → Completed`, `Terminating → Terminated`), guard которого исправляет этот фикс.
- [ADR-006 v.1](../design/ADR-006-events-and-subscriptions.md) — семантика доставки событий / подписок (будущий владелец тайминга регистрации).
- [FIX-001](FIX-001-thresher-eventhub-startup-race.md) — установил, что EventHub запускается до регистрации инстансами.

## 8. Сводка по реализации

Залендено в ветке `fix/event-start-registration` (от `master`). RC1 + RC2 — исправления движка; RC3 — исправление примера.

**Изменения**

- **RC1** — `internal/instance/instance.go:574-580` (`RegisterEvent`): guard «только `Active`» стал `is := inst.State(); if is != Created && is != Active { … "instance is terminal…" }`, разрешая регистрацию на построении (`Created`) и в рантайме (`Active`) и отказывая только терминальным инстансам.
- **RC2** — `pkg/thresher/thresher.go` (`StartProcess`, ~:358-380): мьютекс движка теперь оборачивает только поиск `t.snapshots[processID]` (`:373-374`) и освобождается до `t.launchInstance(s)`; проверка started использует `t.State()` вместо незащищённого чтения `t.state`.
- **RC3** — `examples/timer-event/main.go:106` (`context.WithTimeout(…, 8s)` + `defer cancel()`) и `:126` (простой `<-ctx.Done()` → `Process completed`).

**Добавленные тесты**

- `internal/instance/register_event_test.go` — `TestRegisterEventAllowsCreated` (V1), `TestRegisterEventRejectsTerminal` (V2).
- `pkg/thresher/thresher_process_test.go` — `TestStartProcess_NoReentrantDeadlock` (V3, защищён таймаутом).

**Результаты верификации**

| # | Результат |
|---|---|
| V1 | 🟢 инстанс в `Created` регистрирует определение стартового события — проходит. |
| V2 | 🟢 терминальный инстанс по-прежнему отвергается — проходит. |
| V3 | 🟢 `StartProcess` возвращается без повторной блокировки — проходит. |
| V4 | 🟢 `examples/basic-process` код 0. |
| V5 | 🟢 `examples/timer-event` код 0; таймер срабатывает ~5 с; печатает `Process completed`. |
| V6 | 🟢 `make ci` зелёный (lint 0 проблем, race-тесты, diff-coverage 100 % по 10 затронутым строкам, govulncheck); `examples/simple-timer` код 0. |

**Коммиты по майлстоунам**

- `b7af364` — M1 (RC1): `RegisterEvent` разрешает нетерминальные состояния + тесты V1/V2.
- `0ffa77a` — M2 (RC2): `StartProcess` освобождает `t.m` до `launchInstance` + тест V3.
- `adddea3` — doc: добавлен RC3, опровергнут RC4.
- `004319e` — M3 (RC3): отменяемый контекст в `examples/timer-event`.
- (`9d3e947` — исходный коммит документа с завершённым RCA в ветке.)

## 9. Открытые вопросы

- Ничего блокирующего. (Должен ли тайминг регистрации в итоге переехать в runtime-only — это вопрос ADR-006, не данного FIX.)

## История документа

| Версия | Дата | Автор | Изменение |
|---|---|---|---|
| v.1 | 2026-06-09 | Руслан Габитов | Принято (залендено). `StartProcess` уходит в дедлок из-за трёх дефектов: **RC1** — `RegisterEvent` требует `Active`, но узлы со стартовым событием регистрируются во время `New` (`Created`); **RC2** — `StartProcess` удерживает `t.m` сквозь `launchInstance`, который повторно её захватывает (дедлок повторного неперевходимого `sync.Mutex` — задевает `basic-process` напрямую через `launchInstance:403`, а `timer-event` через `Thresher.State()`); **RC3** — `examples/timer-event` ждёт на `context.Background().Done()` (nil-канал), поэтому уходит в дедлок после того, как таймер корректно сработал. Исправление: разрешить нетерминальные состояния для регистрации (RC1) + сузить `StartProcess`, чтобы освобождать `t.m` до `launchInstance` (RC2) + дать примеру отменяемый контекст (RC3). Оба сломанных примера в объёме. (RCA расширялся через Draft-правки по ходу воспроизведения дедлока: сначала только guard → RC1+RC2; затем, после замера паники на 5.01 с, гипотеза RC4 «движок не планирует таймер» была опровергнута, а оставшийся отказ отнесён к RC3, примеру. Draft-правки, без отдельных строк.) |
