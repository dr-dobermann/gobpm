# GoBPM — BPMN 2.0 Process Engine for Go

![GitHub License](https://img.shields.io/github/license/dr-dobermann/gobpm)
![GitHub Tag](https://img.shields.io/github/v/tag/dr-dobermann/gobpm)
![GitHub go.mod Go version](https://img.shields.io/github/go-mod/go-version/dr-dobermann/gobpm)
[![codecov](https://codecov.io/github/dr-dobermann/gobpm/graph/badge.svg?token=ENKOTEL4VN)](https://codecov.io/github/dr-dobermann/gobpm)
[![Go Report Card](https://goreportcard.com/badge/github.com/dr-dobermann/gobpm)](https://goreportcard.com/report/github.com/dr-dobermann/gobpm)
[![Go Reference](https://pkg.go.dev/badge/github.com/dr-dobermann/gobpm.svg)](https://pkg.go.dev/github.com/dr-dobermann/gobpm)

> EN-оригинал — канонический: [README.md](README.md). Этот файл — его перевод (twin).

**GoBPM** — нативный Go-движок BPMN 2.0. Он спроектирован встраиваться прямо в Go-приложение как минимальная, лёгкая по зависимостям **библиотека** — и масштабироваться до самостоятельного процессного **сервера** через аддитивные runtime-компоненты, не заставляя пользователей библиотеки тащить то, что им не нужно.

> **Статус:** v0.8.0-rc.1 — активная разработка, пока не готово к production.

Видение, область и архитектура определены в [SAD-001](docs/design/SAD-001-vision-and-architecture.md) и его ADR'ах; план поставки — [Development Roadmap](docs/analytics/gobpm%20Development%20Roadmap.md).

## Два пути

1. **Встраиваемая библиотека.** `import github.com/dr-dobermann/gobpm`, собрать движок, зарегистрировать процесс, запустить. Никаких внешних сервисов не требуется.
2. **Самостоятельный runtime.** `gobpm-server` (планируется, модуль `runtime/`) предоставляет движок по HTTP/gRPC с настоящей персистентностью, идентификацией и observability — построенный *на* библиотеке, а не её форк.

Библиотека не несёт runtime-балласта; runtime никогда не переписывает движок заново.

## Ключевые характеристики

- **Библиотека, а не фреймворк** — встраивается в ваш Go-бинарь; ни JVM, ни контейнеров, ни внешних сервисов. Ядро зависит только от stdlib Go + `github.com/google/uuid`.
- **BPMN 2.0 Process Execution Conformance** — Common Executable Subclass плюс расширение ComplexGateway. Авторитетная область: [docs/bpmn-spec/conformance.md](docs/bpmn-spec/conformance.md).
- **Предсказуемая модель выполнения** — одна goroutine event-loop'а на инстанс процесса владеет состоянием; каждый *трек* (поток выполнения) работает в своей goroutine, а токен — это проекция позиции трека, а не хранимый объект; `context.Context` — контракт отмены. См. [ADR-001](docs/design/ADR-001-execution-model.md).
- **Расширяемость через интерфейсы** — персистентность, выражения, обмен сообщениями, observability, авторизация, распределение задач и часы — всё за интерфейсами с дефолтами в ядре. См. [ADR-002](docs/design/ADR-002-extension-architecture.md).
- **Наблюдаемость по умолчанию** — `Logger` по умолчанию `slog.Default()`; вы *отказываетесь* от телеметрии, а не подключаете её. Tracer/metrics по умолчанию no-op (адаптер OpenTelemetry поставляется отдельно).
- **Обработка сообщений и корреляция** — send/receive-задачи и throw/catch message-события через подключаемый брокер; сообщение может **инстанцировать** процесс (event-triggered instantiation) и **коррелировать** к нужному инстансу по ключу, выведенному из payload'а, а **последующее** сообщение маршрутизируется обратно к конкретному выполняющемуся инстансу, к чьей conversation оно относится — по одному или нескольким ключам (conversation-token threading). См. [ADR-014](docs/design/ADR-014-message-handling.md) / [ADR-015](docs/design/ADR-015-event-triggered-instantiation.md) / [ADR-016](docs/design/ADR-016-message-correlation.md).
- **Версионирование определений** — `RegisterProcess` возвращает версионированный дескриптор регистрации; повторная регистрация того же id процесса создаёт новую версию, а старые версии продолжают выполнять уже запущенные инстансы. **Последняя** версия владеет авто-стартом — новая регистрация вытесняет стартеры предыдущей, а снятие последней версии возвращает (промоутит) авто-старт к новейшей оставшейся. Запуск — по дескриптору (`StartProcess`), по новейшей (`StartLatest`) или по конкретной версии (`StartVersion`). См. [ADR-019](docs/design/ADR-019-definition-versioning.md).
- **Программное построение модели** — процессы строятся в Go. Разбор XML намеренно отвязан от модельного слоя.

## Архитектура

```
Process model ──> Snapshot ──> Engine (Thresher) ──> Instance (orchestrator)
   pkg/model        immutable      pkg/thresher          1 goroutine / instance
                    definition                            ├── Tokens (1 goroutine each)
                                                          ├── EventHub + waiters
                                                          └── Scope (hierarchical data)
```

Зависимости текут только вниз; нижние слои ничего не знают о верхних.

### Основные пакеты

| Пакет | Описание |
|---------|-------------|
| `pkg/thresher/` | Фасад движка — реестр процессов и жизненный цикл инстансов |
| `pkg/model/` | Типы элементов BPMN (activities, events, gateways, flow, data, …) |
| `pkg/errs/`, `pkg/set/` | Структурированные ошибки; вспомогательные структуры данных |
| `internal/instance/` | Выполнение instance / track / token (+ `snapshot/`) |
| `internal/eventproc/` | EventHub + event-waiter'ы (timer, …) |
| `internal/scope/` | Иерархическое скоупирование данных и затенение переменных |

## Быстрый старт

```bash
go get github.com/dr-dobermann/gobpm
```

```go
// Start -> ServiceTask -> End  (errors elided for brevity)
engine, _ := thresher.New("demo-engine")

// CreateDefaultStates wires the data states that process properties use.
_ = data.CreateDefaultStates()

// A process-level property the ServiceTask reads at runtime.
proc, _ := process.New("demo-process",
    data.WithProperties(
        data.MustProperty("user_name",
            data.MustItemDefinition(values.NewVariable("dr.Dobermann"),
                foundation.WithID("user_name")),
            data.ReadyDataState)))
start, _ := events.NewStartEvent("start")

// A ServiceTask runs your Go code: gooper.New builds the operation straight
// from a functor. The functor receives a read-only DataReader over process
// data and engine runtime variables (and its optional bound input message —
// nil here, since this operation declares no messages).
op, _ := gooper.New("greet",
    func(ctx context.Context, r service.DataReader, _ *data.ItemDefinition) (*data.ItemDefinition, error) {
        user, _ := r.GetData("user_name")             // a process property
        started, _ := r.GetData("RUNTIME/STARTED_AT") // an engine runtime variable
        fmt.Printf("  ▶ hello, %v (started at %v)\n",
            user.Value().Get(ctx), started.Value().Get(ctx))
        return nil, nil
    })
task, _ := activities.NewServiceTask("work", op, activities.WithoutParams())

end, _ := events.NewEndEvent("end")

_ = proc.Add(start)
_ = proc.Add(task)
_ = proc.Add(end)
_, _ = flow.Link(start, task)
_, _ = flow.Link(task, end)

// RegisterProcess возвращает дескриптор регистрации с (key, version);
// повторная регистрация того же id процесса создаёт новую версию.
reg, _ := engine.RegisterProcess(proc)
_ = engine.Run(context.Background())

// Запуск конкретной зарегистрированной версии по её дескриптору. StartLatest(key)
// и StartVersion(key, n) адресуют по id процесса. Каждый возвращает read-only
// дескриптор выполняющегося инстанса.
inst, _ := engine.StartProcess(reg)

// Block until the instance finishes — the guaranteed completion signal.
state, _ := inst.WaitCompletion(context.Background())
fmt.Println("done:", state) // "Completed"
```

Функтор `gooper` — это то, как вы встраиваете произвольную Go-логику в процесс: здесь он читает свойство процесса и runtime-переменную движка через read-only `DataReader`, и тот же паттерн масштабируется до настоящего обработчика.

`StartProcess` возвращает read-only **`InstanceHandle`** — ваше окно в выполняющийся инстанс: `State()`, живой снимок `Tokens()`, полную `History()` (каждый трек, включая слитые), read-only `Data()` и `WaitCompletion(ctx)` для ожидания завершения. Чтобы следить за прогрессом по мере его развития, подпишите наблюдателя на поток событий жизненного цикла / токенов / узлов инстанса:

```go
// an Observer is any type with OnFact(observability.Fact):
type logger struct{}

func (logger) OnFact(f observability.Fact) {
    fmt.Printf("  • %s %s %s\n", f.Kind, f.Phase, f.NodeName)
}

sub := inst.Observe(logger{})
defer sub.Cancel() // deregister + drain; sub.Dropped() counts any overflow
```

`Fact` несёт `Kind` (EngineState, NodeProgress, JobState, Fault, …), `Phase`, идентичность узла и маскированную мапу `Details` (id/имена/коды, никогда payload). Тот же `Observe` есть и у самого движка — `Thresher.Observe(...)` — чтобы одним потоком следить за **всеми** инстансами плюс фактами уровня движка (регистрация процессов, жизненный цикл hub'а и движка).

Доставка — best-effort и с потерями: медленный наблюдатель отбрасывает факты, а не блокирует движок — поэтому сигнал **завершения** от `WaitCompletion` — единственный гарантированный, никогда не теряемый сигнал.

Полная, запускаемая версия (с обработкой ошибок и ожиданием выполнения задачи) лежит в [`examples/basic-process/`](examples/basic-process/); см. также [`examples/parallel-gateway/`](examples/parallel-gateway/) (конкурентные ветви), [`examples/process-data/`](examples/process-data/) (данные процесса через задачу) и таймер-примеры [`examples/simple-timer/`](examples/simple-timer/) · [`examples/timer-event/`](examples/timer-event/).

По маршрутизирующим шлюзам см. [`examples/gateway-routing/`](examples/gateway-routing/) (исключающий выбор) · [`examples/inclusive-join/`](examples/inclusive-join/) (включающий split + OR-join) · [`examples/complex-gateway/`](examples/complex-gateway/) (join по порогу активации), и **Event-Based**-шлюз — [`examples/event-based-gateway/`](examples/event-based-gateway/) (отложенный выбор по ходу потока: первое сработавшее из нескольких событий выигрывает, остальные отбрасываются) · [`examples/event-based-parallel-start/`](examples/event-based-parallel-start/) (процесс, **запускаемый** event-шлюзом — первое из двух коррелированных сообщений создаёт инстанс, второе перевзводится к нему, и он завершается, когда пришли оба).

По обработке сообщений см. [`examples/message-send-receive/`](examples/message-send-receive/) (SendTask публикует в брокер, ReceiveTask ждёт и связывает payload) · [`examples/message-intermediate-events/`](examples/message-intermediate-events/) (throw/catch message-события), и [`examples/inter-instance-correlation/`](examples/inter-instance-correlation/) — сообщение **инстанцирует** процесс-обработчик и **коррелирует** по ключу, выведенному из payload'а (один инстанс обработчика на отдельный заказ) · [`examples/conversation-routing/`](examples/conversation-routing/) — последующее сообщение **маршрутизируется обратно** к конкретному инстансу-обработчику, к чьей conversation оно относится (keyed in-instance receivers; две conversation'а остаются изолированными).

По signal-событиям (broadcast, без корреляции) см. [`examples/signal-broadcast/`](examples/signal-broadcast/) — один throw достигает **каждого** ожидающего перехватчика в зоне досягаемости · и [`examples/signal-start/`](examples/signal-start/) — broadcast-сигнал **инстанцирует** процессы, чей стартовый триггер — сигнал (один broadcast → один инстанс на каждое signal-start-объявление).

По граничным событиям (прерывание activity) см. [`examples/boundary-events/`](examples/boundary-events/) — **прерывающая таймер-граница** как таймаут на долгой задаче: 2-секундная граница срабатывает раньше, чем закончится ~4-секундная activity, отменяет её и направляет токен на exception-flow границы.

По аварийному завершению процесса см. [`examples/terminate-end-event/`](examples/terminate-end-event/) — **Terminate End Event** на одной из веток параллельного процесса: ветка проверки на мошенничество доходит до него и завершает весь экземпляр, отменяя незаконченный платёж на середине списания — экземпляр оказывается в состоянии `Terminated`, а не `Completed`.

По структурным данным (доступ *внутрь* значения по пути) см. [`examples/structural-data/`](examples/structural-data/) — свойство-**запись** `order` `{id, total, items:[{sku, price}]}`, где service task читает `order.items[0].price` через `DataReader`, а исключающий шлюз маршрутизирует по `order.total` — оба по пути через единый шов доступа к данным (ADR-011 v.6 §2.9); пример [`examples/service-task-worker/`](examples/service-task-worker/) добавляет структурный **output mapping** — worker возвращает структурированное тело, а правила маппинга извлекают вложенные поля (`body.warehouse.zone`). И наоборот, [`examples/structural-output-mapping/`](examples/structural-output-mapping/) показывает путь записи — worker возвращает **плоское** тело, а правила маппинга с общей головой `order` **собирают** одну вложенную запись (со списком `items`, созданным авто-vivify) и читают её обратно по пути (SRD-043). Трио замыкает [`examples/data-change/`](examples/data-change/) — обнаружение изменений через commit-diff: наблюдатель получает по одному факту `DataChange` на каждый изменённый путь при коммитах узлов (SRD-044).

### Логирование при старте

`thresher.New` печатает стартовый отчёт — ASCII-баннер с версией движка и последним коммитом, затем по одной строке на каждое разрешённое расширение — так что обвязка видна в логе в момент конструирования. Оба блока включены по умолчанию; отключайте поблочно, когда шум не нужен:

```go
// Fully silent startup:
eng, _ := thresher.New("worker-7",
    thresher.WithoutBanner(),        // drop the banner / version / commit
    thresher.WithoutStartupConfig(), // drop the per-extension config dump
)
```

## Разработка

```bash
make tools     # one-time: install pinned dev tools (mockery, golangci-lint, govulncheck)
make ci        # full pre-push gate — mirrors GitHub CI exactly (tidy, lint, build, race tests, diff-coverage, vuln scan)

make test         # tests (generates mocks first)
make lint         # lint core module
make build        # build to ./bin/
make cover-check  # diff-coverage gate — changed lines must be >= COVER_MIN (run after `make test-all`)
```

`make ci` — это контракт: зелёный локально ⇒ зелёный на CI. Go-toolchain запинен (`go.mod` → `go1.25.11`), так что локально и на CI сканируется идентичная стандартная библиотека.

### Как мы работаем

- **Spec-first** — нетривиальные изменения начинаются со спецификации (SRD/FIX), ссылающейся на управляющий ADR; спецификация приземляется в том же change-set'е, что и её реализация.
- **`master` защищён** — изменения приземляются только через PR с зелёным `check`; никаких прямых, force- или admin-bypass-пушей.
- **Diff-coverage gate** — CI падает, когда строки, которые изменение *добавляет или модифицирует*, покрыты ниже `COVER_MIN` (сейчас 95%, растёт к 100%). Он судит только изменённые строки, так что бэклог непокрытого нетронутого кода никогда не блокирует PR. См. [SRD-002](docs/srd/SRD-002-ci-diff-coverage-gate.md).
- **Design-доки** под `docs/design/` ([SAD-001](docs/design/SAD-001-vision-and-architecture.md), [ADR-001…007](docs/design/)) — источник истины; см. [CONTRIBUTING.md](CONTRIBUTING.md).

### Требования

- Go (toolchain запинен на `go1.25.11` через `go.mod`; `GOTOOLCHAIN=auto` подтянет его автоматически)
- Dev-инструменты через `make tools`: [mockery v3](https://github.com/vektra/mockery), [golangci-lint v2](https://golangci-lint.run/), [govulncheck](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck)

## Документация

- [Vision & Architecture (SAD-001)](docs/design/SAD-001-vision-and-architecture.md) и [ADR'ы](docs/design/) — концепция
- [Development Roadmap](docs/analytics/gobpm%20Development%20Roadmap.md) — workstream'ы + вехи
- [Conformance scope](docs/bpmn-spec/conformance.md) и [BPMN 2.0 reference KB](docs/bpmn-spec/)
- [Documentation Index](README_INDEX.md) · [API Reference](https://pkg.go.dev/github.com/dr-dobermann/gobpm) · [Contributing](CONTRIBUTING.md) · [Changelog](CHANGELOG.md)

## Лицензия

LGPL-3.0 — см. [LICENSE](LICENSE).
