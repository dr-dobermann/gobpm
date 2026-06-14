# SRD-011 — Go-operation service reader (полиморфная Operation)

| Поле | Значение |
|---|---|
| Статус | Принято |
| Версия | v.1 |
| Дата | 2026-06-14 |
| Владелец | Руслан Габитов |
| Реализует | [ADR-011 v.5 Process Data Flow](../design/ADR-011-process-data-flow.ru.md) |

Этот SRD приземляет [ADR-011 v.5](../design/ADR-011-process-data-flow.ru.md) §2.6: `Operation` у `ServiceTask` становится **полиморфной по локусу исполнения** — **external message operation** (out-of-process `Implementor`, message-only по локусу, отвязанная) и in-process **Go operation**, functor которой получает **узкий публичный read-only `DataReader`** (адресуемые чтения data plane из [ADR-010 v.2 §2.7](../design/ADR-010-process-data-model.ru.md), приземлённые [SRD-010 v.1](SRD-010-addressable-data-access.ru.md)) **и** своё опциональное связанное входное сообщение, **комбинируя** reader-based- и message-based-доступ по выбору автора и **возвращая свой результат**. Расширение Go-operation зарегистрировано в [SAD-001 v.1 §14.2](../design/SAD-001-vision-and-architecture.md).

## 1. Контекст и мотивация

### 1.1 Текущее состояние (сверено с кодом)

- **`Operation` — конкретная структура, message-only.** `service.Operation` (`pkg/model/service/operation.go:41`) держит `implementation Implementor`, `inMessage`/`outMessage *bpmncommon.Message`, набор error-классов, имя и `BaseElement`. Строится `NewOperation(name, inMsg, outMsg, implementor, …)` (`operation.go:52`) / `MustOperation` (`operation.go:93`), клонируется per-instance через `Clone()` (`operation.go:116`), запускается `Run(ctx)` (`operation.go:169`), который вызывает `implementation.Execute(ctx, in)` с `in = inMessage.Item()` и пишет результат в `outMessage`.
- **Go-functor сегодня — это `Implementor`, а не вид.** `gooper.New(f, ers…)` (`pkg/model/service/gooper/gooper.go:34`) возвращает `service.Implementor`, чей `OpFunctor` это `func(ctx, *data.ItemDefinition) (*data.ItemDefinition, error)` (`gooper.go:22`). Он получает **только входной message-item своей operation** — никогда per-execution-окружение. Так что Go-functor не может прочитать process property или runtime-переменную по имени, хотя data plane теперь выставляет ровно это (SRD-010).
- **`ServiceTask` управляет message-хореографией.** `ServiceTask.Exec` (`pkg/model/activities/service_task.go:129`) клонирует operation, вызывает `loadInputMessage` (`service_task.go:165`: `re.GetDataByID(op.IncomingMessage().Item().ID())`, проверка Ready-состояния, обновление `inMessage`), `op.Run(ctx)`, затем `uploadOutputMessage` (`service_task.go:202`: оборачивает `outMessage.Item()` в `Parameter` и `re.Put`'ит его).
- **Data reader, нужный ADR, уже существует внутри.** `renv.RuntimeEnvironment` (`internal/renv/renv.go`) выставляет `GetData(name)`, `GetDataByID(id)`, `GetSources()`, `List(path)` (последние два добавлены SRD-010). `execEnv.GetData → frame.GetData` разрешает `SOURCE/addr` (SRD-010 M2). Но `renv` **внутренний** — user-facing service-код не может его импортировать.
- **У message-аксессоров нет внешних читателей.** `grep` по `IncomingMessage()`/`OutgoingMessage()` находит вызовы **только** в `service_task.go`. `MessageEventDefinition.Operation()` (`pkg/model/events/message.go:67`) имеет **ноль вызовов**. `SendTask` (`send_task.go:11`) и `ReceiveTask` (`receive_task.go:18`) — заглушки с одними полями (нет `Exec`, нет `exec.NodeExecutor`).

### 1.2 Зачем

ADR-011 v.5 §2.6 решает полиморфную `Operation`, разделённую по **локусу исполнения**: внешний (out-of-process) сервис остаётся message-only и отвязанным *по локусу* — он не может получить in-process-reader — тогда как in-process Go-код **комбинирует** методы доступа: его functor получает узкий публичный reader **и** своё опциональное связанное входное сообщение, может объявить выходное сообщение и возвращает результат. Автор выбирает использовать reader, message-I/O или оба. SRD-010 построил адресуемый data plane и выставил его на `renv.RuntimeEnvironment`; этот SRD выставляет **публичное** read-only-лицо его service-коду и переформирует `Operation` в два вида. Радиус поражения мал, потому что у message-аксессоров нет внешних читателей (§1.1).

## 2. Цели и охват

### 2.1 Цели (в охвате)

- **G1.** `service.Operation` — **интерфейс** с единообразным `Execute(ctx, r DataReader) (*data.ItemDefinition, error)` плюс identity/metadata (`ID`/`Name`/`Type`/`Errors`/`Clone`). Две реализации: `messageOperation` (каноническая) и `goOperation` (gobpm-native). Интерфейс **минимален** — message-аксессоры остаются приватными у `messageOperation` (внешне их никто не читает, §1.1).
- **G2.** **Публичный, узкий, read-only `service.DataReader`** (в `pkg/model/service`): `GetData`/`GetDataByID`/`GetSources`/`List` и больше ничего — ни записи, ни жизненного цикла, ни событий. `renv.RuntimeEnvironment` структурно удовлетворяет ему, так что `ServiceTask` передаёт `re` напрямую (без адаптера, без import-цикла — `service` не импортирует `renv`).
- **G3.** **Go operation** комбинирует reader + опциональный message-доступ: `gooper.OpFunctor = func(ctx, r service.DataReader, in *data.ItemDefinition) (*data.ItemDefinition, error)` (`in` — связанный входной message-item, `nil` если не объявлен); `gooper.New(name, f, opts…) (service.Operation, error)` строит её, с опциональными in/out-сообщениями и error-классами через функциональные опции. Её `Execute` связывает входное сообщение из scope (если объявлено), вызывает functor с reader'ом и этим item'ом и возвращает результат (заполняя выходное сообщение, если объявлено). Runtime-переменные читаются по явному пути `RUNTIME/<var>`; process properties по простому имени.
- **G4.** **Message operation** сохраняет сегодняшнее поведение: `Execute` сворачивает в себя bind-input / run-implementation / produce-output-хореографию (реализация по-прежнему видит только своё сообщение). `Implementor` без изменений.
- **G5.** `ServiceTask.Exec` становится **kind-agnostic**: `out, err := op.Execute(ctx, re)`; если `out != nil`, коммитит через `re.Put`. `loadInputMessage`/`uploadOutputMessage` переезжают в `messageOperation`.
- **G6.** Пример демонстрирует Go operation, читающую **process property (простое имя) и runtime-переменную (`RUNTIME/STARTED_AT`)** через reader и возвращающую результат.

### 2.2 Не-цели (отложено, у каждой именованный дом)

- **Layering публичного reader / node-executor-контрактов** (в каком пакете они в итоге живут) — layering ADR (ADR-012). Этот SRD фиксирует *существование и форму* reader'а (ADR-011 v.5 §2.6 решает это здесь); размещение временно в `pkg/model/service`.
- **Observe-from-outside** (вызывающий, инспектирующий данные работающего экземпляра) — ADR-013. Здесь только *in-process*-чтение.
- **Доступ на запись из service-кода** — вне охвата по дизайну: Go operation **возвращает** результат; `ServiceTask` коммитит его как выход activity (ADR §2.6, «no write»).
- **Семантика исполнения `SendTask`/`ReceiveTask`** — пока заглушки; меняется только тип поля их `Operation`. Их исполнители — отдельная работа.
- **Конкретные не-`RUNTIME` data sources** (business/JSON-провайдеры) и их регистрация — отсрочка SRD-010 §2.2 в силе.

## 3. Требования

### 3.1 Функциональные

| # | Требование |
|---|---|
| FR-1 | `service.Operation` — интерфейс: `ID() string`, `Name() string`, `Type() string`, `Errors() []string`, `Clone() Operation`, `Execute(ctx context.Context, r DataReader) (*data.ItemDefinition, error)`. Никаких message-аксессоров на интерфейсе. |
| FR-2 | `service.DataReader` (публичный, `pkg/model/service`): `GetData(name string) (data.Data, error)`, `GetDataByID(id string) (data.Data, error)`, `GetSources() []string`, `List(path string) ([]string, error)`. Зеркалит read-подмножество `renv.RuntimeEnvironment`, так что значение `renv.RuntimeEnvironment` удовлетворяет ему структурно. |
| FR-3 | `messageOperation` (текущая структура, неэкспортируемая) реализует `Operation`. `NewOperation(name, inMsg, outMsg, implementor, …)` и `MustOperation(…)` возвращают `Operation`. `messageOperation.Execute(ctx, r)`: если у `inMessage` есть item, читает его по id через `r` (проверка Ready-состояния) и обновляет структуру сообщения; запускает `implementation.Execute(ctx, inItem)`; сверяет с `outMessage` (сегодняшние правила `Run` — несовпадение наличия/отсутствия есть ошибка); возвращает `outMessage.Item()` (или `nil`). `Implementor` по-прежнему видит только свой message-item. |
| FR-4 | `gooper.OpFunctor = func(ctx context.Context, r service.DataReader, in *data.ItemDefinition) (*data.ItemDefinition, error)`. `gooper.New(name string, f OpFunctor, opts ...Option) (service.Operation, error)` валидирует непустое `name` и ненулевой `f`, возвращает `goOperation`, реализующую `Operation` с `Type() == GoOperType`. Функциональные опции поставляют опциональные входящее/исходящее сообщения и error-классы: `WithInMessage(*bpmncommon.Message)`, `WithOutMessage(*bpmncommon.Message)`, `WithErrors(...string)`. `goOperation.Execute(ctx, r)` связывает входное сообщение из `r` по id (проверка Ready), если объявлено, вызывает `f(ctx, r, in)` (`in` nil, если входного сообщения нет) и возвращает результат — заполняя исходящее сообщение, если объявлено (обёрнуто при ошибке). Старый `Implementor`-возвращающий `gooper.New` удалён. |
| FR-5 | `ServiceTask`: поле `operation` и сигнатуры `NewServiceTask`/`loadInputMessage`/`uploadOutputMessage` используют `service.Operation` (интерфейс). `Exec` становится `op := st.operation.Clone(); out, err := op.Execute(ctx, re); if out != nil { re.Put(wrap(out)) }`; `loadInputMessage`/`uploadOutputMessage` удалены (свёрнуты в `messageOperation`). `re` (`renv.RuntimeEnvironment`) передаётся туда, где ожидается `DataReader`. |
| FR-6 | Радиус поражения только по типу поля (без изменения логики): `events.MessageEventDefinition.operation` + сигнатуры `NewMessageEventDefinition`/`MustMessageEventDefinition`/`Operation()`, `activities.SendTask.Operation`, `activities.ReceiveTask.Operation` меняются с `*service.Operation` / `service.Operation` (структура) на `service.Operation` (интерфейс). |
| FR-7 | Go operation примера `process-data` читает process property по простому имени **и** runtime-переменную по `RUNTIME/STARTED_AT` через reader и возвращает результат; Go-операции `basic-process` и `parallel-gateway` принимают новую сигнатуру `OpFunctor` (reader-only — без объявленных сообщений). |

### 3.2 Нефункциональные

| # | Требование |
|---|---|
| NFR-1 | Поведение message-operation не меняется: существующие тесты `service` / `activities` / `events` / `thresher` проходят; все пять примеров завершаются с exit 0. |
| NFR-2 | `service` не импортирует `internal/renv` (или любой `internal/*`): `DataReader` удовлетворяется структурно, сохраняя публичную поверхность свободной от внутренних типов. |
| NFR-3 | `make ci` зелёный на каждом milestone; diff-coverage ≥95 % (цель 100 %) на затронутых файлах. |
| NFR-4 | Каждый новый/изменённый экспортируемый символ снабжён doc-комментарием; новые конструкторы валидируют входы само-идентифицирующими ошибками (`gooper.New`: nil-functor, пустое имя). |

## 4. Дизайн и план реализации

### 4.1 Полиморфная Operation

```mermaid
classDiagram
    class Operation {
        <<interface>>
        +ID() string
        +Name() string
        +Type() string
        +Errors() []string
        +Clone() Operation
        +Execute(ctx, r DataReader) (*ItemDefinition, error)
    }
    class messageOperation {
        -implementation Implementor
        -inMessage Message
        -outMessage Message
        -errors Set
    }
    class goOperation {
        -f OpFunctor
        -inMessage Message
        -outMessage Message
        -errors Set
    }
    Operation <|.. messageOperation
    Operation <|.. goOperation
    messageOperation ..> Implementor : external; sees only its message
    goOperation ..> DataReader : in-process; reader + optional messages
```

`Execute` единообразен; виды различаются по **локусу**. External message-вид связывает свой `inMessage` из scope (через `GetDataByID` reader'а) и вручает **Implementor**'у только это сообщение — out-of-process-реализация остаётся отвязанной. In-process Go-вид вручает своему **functor**'у reader **и** его опциональное связанное входное сообщение, комбинируя оба метода доступа, и возвращает то, что произвёл. В любом случае `ServiceTask` коммитит возвращённый item.

### 4.2 Reader — это read-подмножество окружения

`DataReader` — это ровно read-only-методы `renv.RuntimeEnvironment`:

```go
// pkg/model/service/datareader.go
type DataReader interface {
    GetData(name string) (data.Data, error)
    GetDataByID(id string) (data.Data, error)
    GetSources() []string
    List(path string) ([]string, error)
}
```

Поскольку набор методов `renv.RuntimeEnvironment` — надмножество, значение этого интерфейса присваиваемо `DataReader`; `ServiceTask.Exec` передаёт `re` напрямую — без адаптера, и `service` никогда не импортирует `renv` (NFR-2).

### 4.3 ServiceTask сворачивается в Execute + Put

```go
op := st.operation.Clone()

out, err := op.Execute(ctx, re) // re satisfies service.DataReader
if err != nil {
    return nil, errs.New(/* operation execution failed */)
}

if out != nil {
    res := data.MustParameter(out.ID(),
        data.MustItemAwareElement(out, data.ReadyDataState))
    if err := re.Put(res); err != nil {
        return nil, errs.New(/* … */)
    }
}

return st.Outgoing(), nil
```

Связывание сообщения (`loadInputMessage`) и обёртка вывода (`uploadOutputMessage`) переезжают дословно в `messageOperation.Execute`; Go-вид сообщений не касается (кроме опциональных, объявленных автором).

### 4.4 Рабочий пример — Go operation, читающая scope (FR-7)

```go
// a reader-only Go operation: read a process property + a runtime variable
// (no messages declared, so the functor's `in` is nil and ignored)
greet, err := gooper.New("greet",
    func(ctx context.Context, r service.DataReader, in *data.ItemDefinition) (*data.ItemDefinition, error) {
        who, err := r.GetData("customer") // process property, plain name
        if err != nil {
            return nil, err
        }

        started, err := r.GetData("RUNTIME/STARTED_AT") // runtime var, by path
        if err != nil {
            return nil, err
        }

        msg := fmt.Sprintf("Hello, %v! (instance started %v)",
            who.Value().Get(ctx), started.Value().Get(ctx))

        return data.NewItemDefinition(values.NewVariable(msg))
    })
// ...
task, err := activities.NewServiceTask("greet-task", greet)
```

Задача, которой *также* нужен message-I/O, объявляет его: `gooper.New("greet", fn, gooper.WithInMessage(in), gooper.WithOutMessage(out))` — тогда `in` functor'а несёт связанный входной item, а возвращённый результат заполняет `out`. При исполнении `ServiceTask` вызывает `greet.Execute(ctx, re)`; functor читает `customer` из default scope и `RUNTIME/STARTED_AT` из источника `RUNTIME` (без столкновения — SRD-010 NFR-2), и возвращённый item коммитится как выход задачи.

### 4.5 Milestones (каждый = один коммит, CI-зелёный)

- **M1 — полиморфная `Operation` + `DataReader` (message-сторона).** Ввести `service.DataReader`; сделать `Operation` интерфейсом; переименовать структуру в `messageOperation` и свернуть хореографию в её `Execute`; `NewOperation`/`MustOperation` возвращают интерфейс; `ServiceTask.Exec` → `Execute`+`Put` (удалить `loadInputMessage`/`uploadOutputMessage`); сменить типы полей в `events.MessageEventDefinition`, `SendTask`, `ReceiveTask`. `Implementor` и `gooper` (всё ещё возвращающий `Implementor`) не тронуты, так что все существующие примеры продолжают компилироваться и проходить — поведение сохранено (FR-1/2/3/5/6, NFR-1).
- **M2 — вид Go-operation + переработка примеров.** `gooper.OpFunctor` получает reader и опциональное входное сообщение; `goOperation` (несущая опциональные in/out-сообщения) реализует `service.Operation`; `gooper.New(name, f, opts…)` возвращает её через функциональные опции (`WithInMessage`/`WithOutMessage`/`WithErrors`); удалить старый `Implementor`-путь. Обновить `basic-process` и `parallel-gateway` (reader-only-сигнатура functor'а) и переработать `process-data` в showcase (FR-4/7). Smoke всех пяти примеров (FR-7, NFR-1).

### 4.6 Тесты (по milestone; детали §5)

Тесты `service` (`Operation` удовлетворена обоими видами; `messageOperation.Execute` связывает вход / производит выход / ошибается при несовпадении; `Clone`), compile-assertion удовлетворения `DataReader` (`var _ service.DataReader = (renv.RuntimeEnvironment)(nil)` во внутреннем тесте, который может импортировать оба), тесты `gooper` (functor получает reader, возвращает результат; nil-functor / пустое имя отвергаются; обёртка ошибок `goOperation.Execute`), тесты `service_task` в `activities` (Execute+Put для обоих видов через stub reader) и пять примеров как smoke.

## 5. Верификация (Definition of Done)

| # | Проверка | Ожидание |
|---|---|---|
| V1 | `service.Operation` — интерфейс, реализованный `messageOperation` и `goOperation`; `NewOperation`/`MustOperation`/`gooper.New` возвращают его (FR-1/3/4). | зелёный |
| V2 | `service.DataReader` публичен и структурно удовлетворён `renv.RuntimeEnvironment` (compile-assertion) (FR-2, NFR-2). | зелёный |
| V3 | Message operation по-прежнему связывает вход из scope, запускает реализацию и производит выход; несовпадение наличия выхода ошибается как раньше (FR-3, NFR-1). | зелёный |
| V4 | Functor Go-operation получает reader, и возвращённый item коммитится `ServiceTask`'ом; чтение property + `RUNTIME/STARTED_AT` работает (FR-4/5/7). | зелёный |
| V5 | `gooper.New` отвергает nil-functor и пустое имя само-идентифицирующими ошибками (NFR-4). | зелёный |
| V6 | Наборы `service` / `activities` / `events` / `thresher` проходят; все пять примеров завершаются с exit 0 (NFR-1). | зелёный |
| V7 | `make ci` зелёный; diff-coverage ≥95 % на затронутых файлах (NFR-3). | pass |

## 6. Риски и регрессии

- **Интерфейсизация широко-удерживаемого типа.** `*service.Operation` держат `events`, `SendTask`, `ReceiveTask`. Изменение — только тип поля; ни один вызывающий не читает message-аксессоры (§1.1), так что поверхность механическая. V6 (наборы + примеры) — подстраховка.
- **Сворачивание хореографии в `messageOperation.Execute`.** Логика bind/run/produce переезжает дословно; контракт `Implementor` не меняется (по-прежнему видит только своё сообщение). V3 утверждает сохранённое поведение.
- **Изменение сигнатуры `gooper`.** `OpFunctor` и `New` меняют форму; все `gooper`-использующие примеры обновляются в том же milestone (M2), так что дерево остаётся зелёным.
- **Дрейф структурного удовлетворения `DataReader`.** Если `renv.RuntimeEnvironment` позже переименует read-метод, `ServiceTask` перестанет компилироваться — compile-time-сигнал, а assertion V2 закрепляет связь.

## 7. Сводка реализации

Приземлено на `feat/go-operation-service-reader` в двух milestone-коммитах (плюс doc-коммиты), всё `make ci`-зелёное со **100 %** diff-coverage на затронутых файлах.

### 7.1 Milestones

| Milestone | Коммит | Охват |
|---|---|---|
| (doc) поправка ADR-011 v.5 | `bd7f41c` | §2.6 разделён по локусу исполнения; in-process комбинирует reader + опциональные сообщения (EN+RU) + SAD §14.2. |
| (doc) выравнивание SRD-011 | `25666c7` | Draft приведён к модели композиции + функциональным опциям. |
| M1 — полиморфная Operation + DataReader | `0b52290` | интерфейс `Operation`; `messageOperation` (Execute сворачивает bind/run/produce); публичный `DataReader`; `ServiceTask` = `Execute`+`Put`; радиус по типу поля. |
| M2 — вид Go-operation + примеры | `2cd4ed8` | Go op в `gooper` (`OpFunctor(ctx, r, in)`, `New(name, f, opts…)`, `WithInMessage`/`WithOutMessage`/`WithErrors`); общий `service.BindInput`; переработка примеров вкл. showcase `process-data`. |

### 7.2 Файлы

- `pkg/model/service/operation.go` — интерфейс `Operation`, `messageOperation`, экспортируемый `BindInput`.
- `pkg/model/service/datareader.go` (новый) — публичный `DataReader`.
- `pkg/model/service/gooper/gooper.go` — `goOperation` (комбинирует reader + опциональные сообщения), функциональные опции.
- `pkg/model/activities/service_task.go` — `Exec` = `Execute`+`Put`.
- `pkg/model/events/message.go`, `pkg/model/activities/{send,receive}_task.go` — тип поля (интерфейс).
- `examples/{basic-process,parallel-gateway,process-data}` — новый functor; `process-data` читает property + `RUNTIME/STARTED_AT`.

### 7.3 Результаты верификации

| Проверка | Результат |
|---|---|
| V1 интерфейс Operation + оба вида | ✅ assertions `var _ Operation/_ service.Operation`; `operation_test`, `gooper_test` |
| V2 DataReader публичен + структурно удовлетворён | ✅ `var _ service.DataReader = (renv.RuntimeEnvironment)(nil)` |
| V3 message op связывает/запускает/производит; несовпадение ошибается | ✅ `operation_test` (bind/produce/mismatch) |
| V4 functor Go op получает reader; результат коммитится; читает RUNTIME/STARTED_AT | ✅ `gooper_test`; smoke `process-data` |
| V5 `gooper.New` отвергает nil-functor / пустое имя | ✅ `gooper_test` |
| V6 наборы проходят; 5 примеров exit 0 | ✅ `make ci`; все примеры отработали |
| V7 `make ci` + diff-coverage 100 % | ✅ |

### 7.4 Отклонения от плана §4

- Go operation больше не message-free (ADR-011 v.5): она комбинирует reader +
  опциональные сообщения, так что `gooper.New` использует функциональные опции,
  а `OpFunctor` несёт связанный входной item. M1 (message-сторона) приземлён без
  изменений.
- Общее связывание входа вынесено в экспортируемый `service.BindInput` (DRY между
  `messageOperation` и `goOperation`).

## 8. Ссылки

- [ADR-011 v.5 Process Data Flow](../design/ADR-011-process-data-flow.ru.md) — §2.6 (полиморфная `Operation`: message-вид + Go-вид с узким публичным reader), которую этот SRD приземляет.
- [ADR-010 v.2 Process Data Model](../design/ADR-010-process-data-model.ru.md) — §2.7 (адресуемый доступ к данным), который reader выставляет; runtime-переменные читаются по `RUNTIME/<var>`.
- [SRD-010 v.1 Addressable data access](SRD-010-addressable-data-access.ru.md) — data plane (`GetData`/`GetDataByID`/`GetSources`/`List` на `renv.RuntimeEnvironment`), который зеркалит `DataReader`; боковая ссылка.
- [SAD-001 v.1 Vision & Architecture](../design/SAD-001-vision-and-architecture.md) — §14.2 регистрирует расширение Go-operation-with-a-data-reader.

## 9. Открытые вопросы

- Нет. Поверхность интерфейса `Operation` (минимальная — без message-аксессоров, подтверждено), размещение `DataReader` (`pkg/model/service`, подтверждено; layering ADR может переместить), единообразная сигнатура `Execute(ctx, DataReader)` с message-хореографией, свёрнутой в `messageOperation`, in-process Go operation, комбинирующая reader + опциональный message-доступ (ADR-011 v.5 — на выбор автора), и `gooper.New(name, f, opts…)` (функциональные опции для сообщений/ошибок), возвращающий Go-вид, — решены выше. Node-level-шов `MessageProducer`/`MessageConsumer`, исполнение `SendTask`/`ReceiveTask` и конкретные не-`RUNTIME`-источники отложены (§2.2; ADR-011 v.5 §2.6).

## История документа

| Версия | Дата | Автор | Изменение |
|---|---|---|---|
| v.1 | 2026-06-14 | Руслан Габитов | Принято (приземлено). Приземляет ADR-011 v.5 §2.6: `service.Operation` становится полиморфным интерфейсом (`Execute(ctx, DataReader)`), разделённым по **локусу исполнения** — каноническая external `messageOperation` (сворачивает bind/run/produce-хореографию; `Implementor` без изменений; message-only по локусу) и in-process gobpm-native `goOperation`, **комбинирующая** reader + опциональный message-доступ (`OpFunctor = func(ctx, r DataReader, in *data.ItemDefinition) …`; `gooper.New(name, f, opts…)` с `WithInMessage`/`WithOutMessage`/`WithErrors`). `DataReader` — read-подмножество `renv.RuntimeEnvironment`, удовлетворённое структурно (без импорта `internal`). `ServiceTask.Exec` сворачивается в `Execute`+`Put`. Радиус поражения по типу поля в `events.MessageEventDefinition`/`SendTask`/`ReceiveTask` (без читателей message-аксессоров). Node-level-шов `MessageProducer`/`MessageConsumer` отложен до SRD исполнителей `SendTask`/`ReceiveTask`. Два milestone (message-сторона интерфейса, сохраняющая поведение → вид Go-operation + showcase-пример, читающий property + `RUNTIME/STARTED_AT`). Реализует ADR-011 v.5. |
