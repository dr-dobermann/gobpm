# FIX-001 — Гонка при старте Thresher / EventHub

> Перевод. Канонична английская версия: [FIX-001-thresher-eventhub-startup-race.md](FIX-001-thresher-eventhub-startup-race.md). При расхождении приоритет у английского текста.

| Поле | Значение |
|---|---|
| Статус | Принято |
| Версия | v.1 |
| Дата | 2026-06-03 |
| Владелец | Руслан Габитов |
| Выявлено из | CI-гейт `-race` (`chore/ci-audit`, `d731895`) поверх мультимодульного / `make ci` каркаса ([SAD-001 v.1 §9](../design/SAD-001-vision-and-architecture.md), [ADR-003 v.1](../design/ADR-003-module-layout.md)) — `-race` теперь гейтит CI; существовавшая гонка становится видимой |
| Связанная концепция | [ADR-001 v.2 Execution Model](../design/ADR-001-execution-model.md) — свобода от гонок это P0-гейт верификации согласно §7 |

## 1. Симптомы

`TestThresher_EventQueueProcessing/event_queue_processes_registered_events` (в `pkg/thresher/thresher_events_test.go`) **нестабилен** под `-race`:

```
go test -race -count=5 -run TestThresher_EventQueueProcessing ./pkg/thresher/
# → 2/5 прогонов падают с:
#   WARNING: DATA RACE
#   testing.go:1617: race detected during execution of test
```

Гонка срабатывает недетерминированно — иногда тест проходит, иногда детектор гонок её фиксирует. Сама логика теста корректна (запустить Thresher, затем зарегистрировать событие); гонка находится в последовательности старта движка и маскировалась таймингом на быстрых прогонах, а также прежним отсутствием `-race` в CI до коммита `d731895` (`chore/ci-audit`).

`-race` теперь гейтит CI (добавлен в `chore/ci-audit` `d731895`, встроен в `test-all` у `make ci` мультимодульным каркасом — см. [ADR-003 v.1](../design/ADR-003-module-layout.md)). Без устранения этой гонки CI на master падает периодически.

## 2. Анализ первопричины

В гонке участвуют два несинхронизированных скалярных поля общей структуры `EventHub`: `started bool` и `ctx context.Context`. Поле-map `waiters` защищено мьютексом корректно (см. использование `eh.m.Lock/RLock`); два скаляра — НЕТ.

### 2.1 Конкурирующие записи (фоновая горутина)

`internal/eventproc/eventhub/eventhub.go:48-60`:

```go
func (eh *EventHub) Run(ctx context.Context) error {
    if eh.started { ... }     // строка 49 — чтение started (без мьютекса)
    eh.started = true         // строка 55 — ЗАПИСЬ started (без мьютекса)
    eh.ctx = ctx              // строка 56 — ЗАПИСЬ ctx (без мьютекса)
    <-ctx.Done()
    return ctx.Err()
}
```

Этот `Run` исполняется в горутине, запущенной из `Thresher.Run`:

`pkg/thresher/thresher.go:184-190`:

```go
// Run eventhub in background
go func() {
    _ = t.eventHub.Run(ctx)   // строка 186 — Run исполняется в этой горутине
}()

// Give eventhub a moment to initialize
time.Sleep(1 * time.Millisecond)   // строка 190 — НАДЕЖДА НА ТАЙМИНГ, НЕ БАРЬЕР
```

### 2.2 Конкурирующие чтения (горутина вызывающего)

После сна в 1 мс вызывающий код обращается к `Thresher.RegisterEvent`, который вызывает `EventHub.RegisterEvent`:

`internal/eventproc/eventhub/eventhub.go:70-113`:

```go
func (eh *EventHub) RegisterEvent(...) error {
    if !eh.started { ... }    // строка 70 — ЧТЕНИЕ started (без мьютекса)
    ...
    if err := w.Service(eh.ctx); err != nil { ... }   // строка 113 — ЧТЕНИЕ ctx (без мьютекса)
    ...
}
```

### 2.3 Отчёт детектора гонок (исчерпывающее доказательство)

```
WARNING: DATA RACE
Read at 0x...4f8 by goroutine 10:
    EventHub.RegisterEvent()  eventhub.go:70
    Thresher.RegisterEvent()  thresher.go:222
    test func1()              thresher_events_test.go:225

Previous write at 0x...4f8 by goroutine 11:
    EventHub.Run()            eventhub.go:55
    Thresher.Run.func1()      thresher.go:186

(второй отчёт о гонке по смещению ...4b0 — той же формы, но по eh.ctx,
 чтения в eventhub.go:113 против записи в eventhub.go:56)
```

Оба конкурирующих поля — простые скаляры, к которым обращаются из разных горутин без `sync.Mutex`, без `sync/atomic`, без рукопожатия через канал. `time.Sleep(1 * time.Millisecond)` в вызывающем коде — это не синхронизация: он даёт фоновой горутине *время* на исполнение, но не *вынуждает* её исполниться, и даже если бы вынуждал, модель памяти Go не гарантировала бы видимость записей без надлежащих примитивов синхронизации.

### 2.4 Почему это стало видимым сейчас (а не раньше)

- `-race` был добавлен в CI в `d731895` (chore/ci-audit, смёржен до этой работы).
- Гонка зависит от тайминга. Прогоны CI с тех пор, вероятно, везло на тайминге.
- Лендинг мультимодульного каркаса добавил `make ci` с race-гейтом для `test-all`, плюс правила depguard. Запуск `make ci` локально и на CI надёжнее провоцирует тайминг, при котором гонка срабатывает.

Хрупкость была всегда; race-гейтнутый `make ci` просто перестал давать ей проскальзывать.

## 3. Решение

Два жизнеспособных подхода. Решение A предпочтительно (более чистое разделение ответственности; соответствует замыслу ADR-001); Решение B — запасной вариант с меньшим диффом.

### 3.1 Решение A (предпочтительное) — разделить `Start` и `Run`

Ввести новый метод `EventHub.Start(ctx)`, который выполняет синхронную инициализацию (`started = true`, `ctx = ctx`), и свести `Run(ctx)` к одному только блокирующему телу цикла событий. `Thresher.Run` вызывает `Start` синхронно перед запуском фоновой горутины и убирает сон в 1 мс.

Набросок:

```go
// internal/eventproc/eventhub/eventhub.go
func (eh *EventHub) Start(ctx context.Context) error {
    if eh.started {
        return errs.New(errs.M("eventHub is already started"), ...)
    }
    eh.started = true
    eh.ctx = ctx
    return nil
}

func (eh *EventHub) Run(ctx context.Context) error {
    if !eh.started {
        return errs.New(errs.M("eventHub not started"), ...)
    }
    <-ctx.Done()
    return ctx.Err()
}
```

```go
// pkg/thresher/thresher.go (фрагмент Run)
t.ctx = ctx

if err := t.eventHub.Start(ctx); err != nil {       // СИНХРОННО
    return err
}

go func() {
    _ = t.eventHub.Run(ctx)                          // ФОНОВО, но старт уже выполнен
}()

// time.Sleep(1ms) УДАЛЁН — больше не нужен.

return t.UpdateState(Started)
```

Почему это предпочтительное исправление:

- `started` и `ctx` записываются **синхронно в родительской горутине, до запуска фоновой горутины**. Это делает записи безопасными для чтения с обеих сторон без мьютекса:
  - Чтения из запущенной горутины (например, `eh.Run`, проверяющий `eh.started`) видят записи через **ребро happens-before, создаваемое запуском горутины** в модели памяти Go — любая запись в родителе до `go f()` видна `f`.
  - Чтения из родительской горутины (и любой горутины, разделяющей ссылку на EventHub, которая была опубликована *после* возврата из `Start` — например, Thresher, чей `Run` успешно вернулся) видят записи через обычную последовательную согласованность в порядке программы родителя, а затем через синхронизацию, опубликовавшую ссылку на EventHub.
  - Map `waiters` остаётся защищённым мьютексом как и сейчас; это исправление не меняет паттерн доступа к нему.
- Убирает `time.Sleep(1 * time.Millisecond)` — это был code smell, а теперь очевидно неверный.
- Чисто разделяет ответственности: `Start` — шаг инициализации (возвращает ошибки при неверной настройке); `Run` — блокирующий цикл (возвращается, когда ctx завершён).
- Соответствует замыслу топологии каналов из ADR-001 §4.3: у движка есть явные фазы настройки до запуска фоновых горутин.
- Интерфейс `EventHub` в `internal/eventproc/eventproc.go` внутренний; добавление метода в его реализацию по умолчанию не ломает публичный API.

### 3.2 Решение B (запасное) — защита через atomic

Если разделение `Start`/`Run` по какой-либо причине нежелательно (например, сам интерфейс `EventHub` пришлось бы дополнять методом `Start`), альтернатива с меньшим диффом защищает два скаляра через `sync/atomic`:

```go
type EventHub struct {
    started atomic.Bool
    ctxVal  atomic.Value   // хранит context.Context
    ...
}

func (eh *EventHub) Run(ctx context.Context) error {
    if !eh.started.CompareAndSwap(false, true) {
        return errs.New(errs.M("already started"), ...)
    }
    eh.ctxVal.Store(ctx)
    <-ctx.Done()
    return ctx.Err()
}

func (eh *EventHub) RegisterEvent(...) error {
    if !eh.started.Load() { ... }
    ...
    ctx, _ := eh.ctxVal.Load().(context.Context)
    if err := w.Service(ctx); err != nil { ... }
}
```

Сон в 1 мс в `Thresher.Run` всё равно нужно убрать — atomic-защита успокаивает детектор гонок, но не устраняет того факта, что `RegisterEvent` может выполниться *раньше* `Run`, если горутина ещё не была запланирована. Замените сон небольшим рукопожатием (например, сигналом из `Run` по буферизованному каналу после завершения `started.Store(true)`; `Thresher.Run` читает-или-таймаутит).

Это решение сохраняет текущую форму интерфейса `EventHub`, но вносит дополнительную сложность рукопожатия. Решение A строго лучше.

### 3.3 Решение

**Принимаем Решение A.** Интерфейс `EventHub` (в `internal/eventproc/eventproc.go`) сейчас экспонирует только `RegisterEvent` / `UnregisterEvent` / `PropagateEvent` / `Run` / `RemoveWaiter`. Добавление `Start` в этот интерфейс (и в реализацию по умолчанию) — небольшое внутреннее изменение. `Run` у thresher становится чище; сон исчезает.

## 4. Верификация

| Что | Как |
|---|---|
| **Гонка устранена** | `go test -race -count=100 -run TestThresher_EventQueueProcessing ./pkg/thresher/` проходит 100/100. (До исправления: ожидалось ~40 падений на цикле из 100 прогонов.) |
| **Нет регрессии в других тестах Thresher** | `go test -race ./pkg/thresher/...` чисто. |
| **Всё ядро чисто от гонок под нагрузкой** | `make test-all` чисто; дополнительно `go test -race -count=10 ./...` чисто (покрывает остальное ядро повторными прогонами). |
| **EventHub по-прежнему отвергает двойной старт** | Новый юнит-тест: `eh.Start(ctx)` дважды → второй вызов возвращает ошибку "already started". |
| **EventHub по-прежнему отвергает RegisterEvent до Start** | Существующий путь теста, покрывающий "eventHub isn't started", остаётся зелёным. |
| **Не осталось логики тестов, зависящей от тайминга** | Код-ревью подтверждает, что `time.Sleep(1 * time.Millisecond)` удалён из `Thresher.Run`. |
| **CI проходит после исправления** | `make ci` отрабатывает чисто локально; проверка GitHub Actions проходит на merge-коммите. |

Гейт приёмки для перевода этого FIX в Принято: race-стресс `-count=100` на регрессионном тесте закоммичен (или заскриптован в CI) и проходит.

## 5. Профилактика

Две общепроектные привычки, которые поймали бы это раньше:

1. **`-race` в `go test` с первого дня CI.** Теперь действует согласно chore/ci-audit. Этот FIX подтверждает данную политику.
2. **Соглашение: любое поле, к которому обращается горутина, запущенная в методе, должно быть либо**:
   - записано **до** запуска горутины (видимо через happens-before запуска горутины), либо
   - защищено примитивом `sync` (Mutex, atomic, канал).

   Никакой `time.Sleep` никогда не считается синхронизацией. Везде, где `time.Sleep` появляется в не-тестовом коде, код-ревью должен спрашивать: "на что это надеется и какой здесь реальный примитив синхронизации?"

Линтер помог бы обеспечить п.2, но ни один готовый не ловит именно этот паттерн. Рассмотрите однострочную внутреннюю проверку: grep по `time.Sleep` в не-тестовых Go-файлах и ревью каждого вхождения на этапе PR.

## 6. Анализ регрессий

Изменение состоит в том, что `EventHub` добавляет метод `Start`, а `Thresher.Run` вызывает его синхронно перед запуском фоновой горутины. Риски:

- **Другие вызывающие `EventHub.Run`** — единственный продакшн-вызывающий (`Thresher.Run`). Поиск подтверждён через `grep -rn 'eventHub.Run\|EventHub\.\|eh\.Run'` (результаты: только `thresher.go` и тесты). Тесты, которые мокают `EventHub` (через mockery), нуждаются в добавлении ожидания `Start` — небольшое механическое обновление.
- **Сгенерированные mockery моки** — интерфейс `EventHub` получает `Start(ctx) error`. Перегенерировать `mockery`, чтобы обновить `generated/mockeventproc/MockEventHub.go`. Любой тест, ранее утверждавший только ожидания `Run`, теперь нуждается также в ожидании `Start`. Список таких тестов ограничен (grep `MockEventHub` в `pkg/thresher/*_test.go` и `internal/eventproc/eventhub/*_test.go`).
- **Другие пути через `eventHub.started` / `eventHub.ctx`** — существующая проверка `eh.started` в `Run` (строка 49) становится избыточной, если `Start` — единственная точка входа, которая его устанавливает. Очистка: в `Run` заменить `if eh.started { ... return already-started-error }` на `if !eh.started { ... return not-started-error }`. Два сообщения об ошибке меняются местами — убедитесь, что существующие тесты, сверяющие текст ошибки, обновлены.
- **Поведение при двойном вызове `Run`** — раньше двойной `Run` выдавал ошибку на втором вызове через проверку `eh.started`. С Решением A `Run` больше не устанавливает `started`; он только его проверяет. Двойной `Run` становится другим путём ошибки. Подтвердите, что существующий тест на "already started" по-прежнему проходит (должен — теперь проверку делает `Start`, а `Thresher.Run` вызывает `Start` один раз).

## 7. Связанное

- [ADR-001 v.2 Execution Model §7](../design/ADR-001-execution-model.md) — свобода от гонок — гейт верификации №1; этот FIX — первый конкретный платёж по этому гейту.
- [SAD-001 v.1 §9 Module Layout](../design/SAD-001-vision-and-architecture.md) + CI-гейт `-race` (`chore/ci-audit` `d731895`) — мультимодульный каркас `make ci` и его `-race`-гейт и есть то, что выявляет эту и подобные ошибки.
- [ADR-003 v.1 §4.6 шаг 3](../design/ADR-003-module-layout.md) — когда интерфейс `EventHub` переедет из `internal/eventproc/` в `pkg/messaging/` (по плану миграции), метод `Start` переедет вместе с ним. Этот FIX выше по течению относительно того продвижения; изменение должно произойти сначала здесь, чтобы зафиксировать race-free дизайн до заморозки публичного интерфейса.
- (Возможный follow-up) Аудит других последовательностей старта движка на похожие гоночные паттерны "Run-в-горутине-затем-sleep". Скорее всего, в других местах их нет, но один разовый проход стоит сделать.

## 8. Сводка по реализации

Решение A (по §3.1) реализовано как единый change-set в ветке `fix/eventhub-startup-race`. Отклонений от §3 нет — `Start` добавлен во внутренний интерфейс `EventHub` и реализован на дефолтном `*EventHub`; `Run` сведён к блокирующему телу цикла событий; `Thresher.Run` вызывает `Start` синхронно перед запуском фоновой горутины; `time.Sleep(1 * time.Millisecond)` удалён.

Затронутые файлы:

- `internal/eventproc/eventproc.go` — добавлен `Start(ctx context.Context) error` в интерфейс `EventHub` с doc-комментариями, ссылающимися на FIX-001 за обоснованием.
- `internal/eventproc/eventhub/eventhub.go` — реализован `Start`; `Run` теперь гардит на `!eh.started` (было: гардил на `eh.started`); записи флага started и ctx переехали из `Run` в `Start`. Сообщения об ошибках поменялись соответственно (`Run` возвращает `"eventHub isn't started"` при вызове до Start, согласованно с существующей формулировкой `RegisterEvent`/`UnregisterEvent`/`PropagateEvent`).
- `pkg/thresher/thresher.go` — блок «запустить горутину, затем поспать» заменён на синхронный вызов `t.eventHub.Start(ctx)` перед `go func() { eh.Run(ctx) }()`. Импорт `time` убран — других использований в этом файле нет.
- `internal/eventproc/eventhub/eventhub_base_test.go` — прежний `TestRun` разделён на `TestStart` (успешный старт, ошибка двойного старта) и `TestRun` (ошибка запуска до старта, запуск с таймаутом, запуск с отменой). Базовые тесты ошибок `Register/Unregister/PropagateEvent` перестали запускать `Run` в горутине + `time.Sleep` и вызывают `hub.Start(ctx)` синхронно.
- `internal/eventproc/eventhub/eventhub_timer_test.go` и `eventhub_message_test.go` — та же замена «горутина+sleep» → синхронный `Start`.
- `generated/mockeventproc/mock_EventHub.go` — перегенерирован через `make gen_mock_files`, чтобы открыть новую mock-поверхность `Start`.

Доказательства верификации (прогон на HEAD ветки перед коммитом):

| Команда | Результат |
|---|---|
| `go test -race -count=100 -run TestThresher_EventQueueProcessing ./pkg/thresher/...` | 300/300 проходов (3 подтеста × 100 итераций) — до исправления этот цикл давал ~40 падений детектора гонок |
| `go test -race ./pkg/thresher/...` | 30 тестов проходят |
| `go test -race ./internal/eventproc/...` | 34 теста проходят по 3 пакетам |
| `make ci` | зелёный (tidy-check-all, lint-all-modules, build-all, test-all, vuln) |

Race-стресс `-count=100` задокументирован выше как гейт приёмки, а не закоммичен как постоянный тест (запуск его на каждом вызове CI умножил бы время тестов ядра на ~100× ради одной конкретной регрессии). Команда есть в этом документе и в таблице верификации §4; ревьюеры, проверяющие исправление, должны прогнать её один раз.

Ветка и коммиты:

- Ветка: `fix/eventhub-startup-race`
- Коммиты: этот FIX-документ (`c601683`) + коммит реализации `70fa5f5`
  (`fix(eventhub): split Start/Run to remove Thresher startup race (FIX-001)`).

План перевода статуса:

- [x] Реализация закоммичена в `fix/eventhub-startup-race`.
- [x] PR #108 смёржен в `master` (merge-коммит `28aa6b9`). Воркфлоу `check`
      на этом мерже был красным из-за несвязанной существовавшей находки
      govulncheck (stdlib-уязвимости), устранённой сразу после пином
      тулчейна go1.25.11 в PR #109; `check` зелёный на `master` по
      состоянию на `3705717`.
- [x] Переведён Draft → Принято. Гейт приёмки перепрогнан на HEAD `master`
      `3705717`: `-race -count=100` чисто. SHA зафиксированы в Истории документа.

## История документа

| Версия | Дата | Автор | Изменение |
|---|---|---|---|
| v.1 | 2026-06-06 | Руслан Габитов | Принято. Реализация смёржена в `master` через PR #108 (коммит реализации `70fa5f5`, merge `28aa6b9`). Гейт приёмки перепроверен на `master` `3705717`: `go test -race -count=100 -run TestThresher_EventQueueProcessing ./pkg/thresher/...` чисто (100/100); воркфлоу `check` зелёный на `master` на `3705717` (сборка мержа PR #108 была красной из-за несвязанной stdlib-находки govulncheck, устранённой пином тулчейна go1.25.11 в PR #109). Предшествующая Draft-итерация свёрнута в эту версию без построчных строк истории. |
