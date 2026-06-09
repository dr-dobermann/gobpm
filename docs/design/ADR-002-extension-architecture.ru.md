# ADR-002 — Архитектура расширений

| Поле | Значение |
|---|---|
| Статус | Принято |
| Версия | v.1 |
| Дата | 2026-06-09 |
| Владелец | Руслан Габитов |
| Замещает | — |
| Уточняет | [SAD-001 v.1 §11 Extension Model](SAD-001-vision-and-architecture.md) |

> EN-оригинал — канонический: [ADR-002-extension-architecture.md](ADR-002-extension-architecture.md). Этот файл — его перевод (twin).

## 1. Контекст

SAD-001 §11 назвал каталог расширений и сказал «Go-идиоматично: интерфейсы + функциональные опции». Этот ADR фиксирует набор интерфейсов, паттерн сборки, разделение public/internal, политику реализаций-по-умолчанию и конвенции для adapter-модулей.

Текущий код уже содержит частичную поверхность расширений, в основном в `internal/`:

| Существующий интерфейс | Расположение | Роль |
|---|---|---|
| `EventHub`, `EventProducer`, `EventProcessor`, `EventWaiter` | `internal/eventproc/eventproc.go` | Модель распределения событий |
| `Scope`, `NodeDataLoader`, `NodeDataConsumer`, `NodeDataProducer` | `internal/scope/scope.go` | Иерархическое скоупирование данных |
| `RuntimeEnvironment` (композит из `Scope + InstanceID + EventProducer + RenderRegistrator`) | `internal/renv/renv.go` | Контекст-«мешок» на каждый instance |
| `Interactor`, `Registrator`, `RenderController` | `internal/interactor/interactor.go` | Абстракция взаимодействия с человеком |
| `FormalExpression`, `Source`, `PropertyAdder` | `pkg/model/data/` | Интеграция BPMN-выражений / данных |

Фасад движка:

- `pkg/thresher/thresher.go` экспонирует `Thresher.New(id string)` — конструктор с одним аргументом; **нет способа внедрить расширения при конструировании**.
- `Thresher` запускает реестр инстансов + регистрацию событий, но не имеет точки внедрения Repository, Logger, Tracer, Clock или любой другой инфраструктуры.

**Отсутствующие точки расширения** (по SAD-001 §11): `Repository`, `Logger`, `Tracer`, `MetricsRecorder`, `Clock`, `MessageBroker`, `AuthorizationProvider`, `WorkerDispatcher`. Ни у одного из них пока нет интерфейса нигде.

Решение ниже устанавливает каталог, разделение public/internal, паттерн сборки и то, как существующая поверхность расширений эволюционирует, чтобы вписаться в него.

## 2. Решение

**Двухслойная модель расширений. Расширения уровня движка регистрируются однократно при конструировании `Thresher` через функциональные опции; контекст уровня instance (существующий `RuntimeEnvironment`) собирается на каждый instance из расширений уровня движка плюс концерны, скоупированные по instance. Все интерфейсы расширений живут в `pkg/`; реализации-по-умолчанию поставляются в ядре; production-реализации живут в модулях `adapters/*` по SAD-001 §9.2.**

Сводная таблица:

| Концерн                     | Механизм                                                                                                                                                                |
| --------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Владение каталогом расширений | Этот ADR определяет полный набор. Будущие дополнения изменяют этот ADR (bump версии).                                                                                   |
| Public-поверхность              | Каждый заменяемый интерфейс живёт в `pkg/`; точная раскладка подпакетов отложена в ADR-003.                                                                                |
| Internal-only поверхность       | «Клей» реализации (например, `EventWaiter`, `NodeDataLoader`) остаётся в `internal/`.                                                                                        |
| Сборка                    | `thresher.New(id string, opts ...thresher.Option) (*Thresher, error)` + функциональные опции. Вызов без опций `thresher.New("id")` даёт рабочий движок — каждая опция просто переопределяет дефолт. **Нет отдельного конструктора `NewDefault`**; дефолты и есть дефолты. |
| Композиция на каждый instance    | Существующий интерфейс `RuntimeEnvironment` — который Instance уже реализует — **расширяется** новыми методами-сервисами уровня движка. Track получает одну внешнюю ссылку при конструировании: свой владеющий `*Instance`. Места вызова из track'а единообразны: `t.inst.Scope().GetData(...)` для instance-локального состояния и `t.inst.Logger().Info(...)` для сервисов движка. Instance-локальные поля принадлежат напрямую Instance; методы уровня движка на Instance — однострочные делегаты к конфигу движка. Рантайм-значения принадлежат движку (Engine), экспонируются через Instance посредством композиции. |
| Кросс-адаптерная композиция   | Адаптеры НЕ зависят от пакетов друг друга. По умолчанию адаптер **разделяет ресурсы движка через внедрённый `EngineRuntime`** (опциональный хук `RuntimeAware` — §3.5 Паттерн C / §8.3): например, AuthZ использует `rt.Repository()`, если ему явно не дали собственный. Пользователь МОЖЕТ **разделить** их, передав опцию на уровне адаптера. См. §3.5 / §4.6. |
| Политика реализаций-по-умолчанию         | Дефолты уровня движка поставляются в ядре, видимые-по-умолчанию (Logger = `slog.Default()` по политике проекта); production подменяет через опции `WithXxx`, тянущие из `adapters/*`. |
| Контракт стабильности          | Каждый публичный интерфейс расширения — semver-стабильный контракт, как только он Принят. Ломающие изменения → новый ADR + bump версии.                                                    |

## 3. Рассмотренные альтернативы

### 3.1 Механизм загрузки плагинов

| Вариант | Описание | Вердикт |
|---|---|---|
| **Go-пакет `plugin` (.so-файлы)** | Движок dlopen()-ит shared-объекты в рантайме; пользователи компилируют свой плагин отдельно. | Отклонено. Поддержка Mac/Windows частичная; совместимость версий хрупкая; конфликтует с ценностным предложением статичных бинарников Go; неидиоматично. |
| **Generic-интерфейсы + связывание на этапе компиляции** — выбрано | Пользователи реализуют Go-интерфейсы; передают реализации в конструктор движка; стандартный `go build` производит один бинарник. | Выбрано. Нативный Go-паттерн; работает на всех платформах; отлаживаемость и наблюдаемость — обычная Go-семантика. |
| **gRPC sidecar-плагины (стиль Hashicorp)** | Плагины — отдельные процессы, общение с которыми идёт через gRPC. | Отклонено для v.1. Тяжеловесно; вводит IPC-режимы отказа для того, что по сути является in-process библиотекой. Может быть переоценено для distribution-истории слоя рантайма (по [SAD-001 §13](SAD-001-vision-and-architecture.md)), но не для ядра. |

### 3.2 Паттерн сборки

| Вариант | Описание | Вердикт |
|---|---|---|
| **Паттерн Builder** (`thresher.NewBuilder().WithLogger(l).Build()`) | Fluent builder-API | Отклонено. Больше церемонии; сложнее добавлять опции, не ломая набор методов билдера; менее Go-идиоматично. |
| **Config-структура** (`thresher.New(cfg Config)`) | Единая config-структура, держащая всё | Отклонено. Вынуждает пользователей конструировать (и zero-инициализировать или частично заполнять) толстую структуру; скрывает, какие поля обязательны, а какие опциональны; слияние дефолтов неуклюже. |
| **Функциональные опции** — выбрано | `thresher.New(id, WithLogger(l), WithRepository(r), ...)` | Выбрано. Идиоматичный Go; тривиально добавлять новые опции без слома API; каждая опция самодокументируема; дефолты явны. |
| **Цепочка методов после конструирования** (`thresher.New(...).WithLogger(l)`) | Мутирующие builder-методы после конструирования | Отклонено. Состояние движка хрупкое во время мутации; риск гонок; позволяет частично сконфигурированному движку стартовать. |

### 3.3 Разделение public/internal для существующих интерфейсов

| Вариант | Описание | Вердикт |
|---|---|---|
| **Держать все расширения в `internal/`** | Внешние пользователи не могут их реализовать; только внутрипроектное использование | Отклонено. Сводит на нет видение embeddable + extensible из SAD-001. |
| **Перенести ВСЕ существующие интерфейсы в `pkg/`** | Оптовый перенос | Отклонено. Некоторые (`EventWaiter`, `NodeDataLoader`) — это «клей» реализации, тесно связанный с внутренностями движка; экспонировать их как контракты стабильности — это over-commitment. |
| **Выборочно экспонировать то, что является настоящей точкой расширения** — выбрано | Промоутим интерфейсы, которые пользователи реально заменяют; «клей» реализации держим приватным | Выбрано. По §4 ниже. |

### 3.4 Локальность реализации-по-умолчанию

| Вариант | Описание | Вердикт |
|---|---|---|
| **Все дефолты в ядре; `New` применяет их, опции переопределяют** — выбрано | `thresher.New(id)` без опций даёт рабочий движок со всеми связанными дефолтами; каждый `WithXxx(...)` переопределяет один дефолт | Выбрано. Использование «из коробки» — основной путь; адаптеры тянутся по необходимости. Нет отдельной функции `NewDefault` — `New` И ЕСТЬ дефолт. |
| **Без дефолтов; пользователь обязан связать всё** | Ядро поставляет только интерфейсы; каждое расширение явно | Отклонено. В худшем случае — связывание 10 расширений ради примера `Hello World`. Ломает цель usability «из коробки» из SAD-001. |
| **Дефолты в модуле `gobpm-defaults`** | Дефолты живут в отдельном sibling-модуле | Отклонено. Добавляет накладные расходы модуля без реального выигрыша; пользователи всё равно хотят `New` без опций в ядре. |

### 3.5 Композиция зависимостей адаптера

Когда адаптеру нужен сервис, который движок уже держит (например, AuthZ-адаптер, который хочет персистить свою политику в том же хранилище, что использует движок), общий случай должен быть **разделением без церемонии**, с явным **разделением (split)** по желанию. Варианты:

| Вариант | Описание | Вердикт |
|---|---|---|
| **A. Рантайм service-locator на конкретном движке** | Адаптер держит ссылку на конкретный `*Thresher` и вызывает `engine.Repository()` в рантайме. | Отклонено. Связывает адаптер с конкретным типом движка; магия «откуда это берётся?»; трудно подделать в изоляции. |
| **B. Явная композиция пользователем** | Пользователь конструирует разделяемый ресурс один раз и продёргивает его и в адаптер, и в движок. | Валидно — оставлено как вариант полной изоляции. Наиболее явный; но это церемония для общего случая «просто разделить дефолт движка». |
| **C. Внедрённый интерфейс `EngineRuntime`** — выбрано | Движок внедряет свой разрешённый `EngineRuntime` (core-*интерфейс* — §4.3) в адаптеры, которые opt-in через опциональный хук `RuntimeAware` (§8.3), на этапе сборки `thresher.New`. Адаптер тянет любую зависимость, которая ему явно не дана, из рантайма (`rt.Repository()`); явная опция на уровне адаптера её переопределяет (split). | Выбрано. По умолчанию = адаптер разделяет ресурсы движка без связывания; split = собственная опция адаптера. Хэндл — это core-интерфейс, внедрённый при сборке (не конкретный движок, тянутый в рантайме), так что адаптер остаётся unit-тестируемым — подделайте `EngineRuntime`. Это сохраняет удобство Варианта A, растворяя его возражение про связывание/тестируемость. |

Пример (Паттерн C):

```go
// Default — AuthZ shares the engine's repository, zero ceremony.
// casbin.Authorizer implements RuntimeAware; the engine injects its
// EngineRuntime at New, and the adapter uses rt.Repository() for its storage.
authz, _ := casbin.NewAuthorizer()
engine, _ := thresher.New("my-engine",
    thresher.WithRepository(repo),                 // the app's default repo
    thresher.WithAuthorizationProvider(authz),     // engine injects EngineRuntime -> authz uses repo
)

// Split — give AuthZ its own store explicitly; the engine skips the injection.
authz, _ := casbin.NewAuthorizer(casbin.WithStorage(otherRepo))
engine, _ := thresher.New("my-engine",
    thresher.WithRepository(repo),
    thresher.WithAuthorizationProvider(authz),     // authz uses otherRepo, not repo
)
```

Адаптер импортирует только core-интерфейсы `EngineRuntime` + `Repository` — никогда не конкретный движок, никогда другой адаптер. Пользователь сохраняет полный контроль, чтобы разделить через собственную опцию адаптера.

## 4. Детали решения

### 4.1 Двухслойная модель расширений

```
┌──────────────────────────────────────────────────────────────────┐
│                      Engine-level extensions                      │
│  (registered once at thresher.New via functional options;         │
│   scope = all instances of the engine; lifetime = engine lifetime)│
│                                                                   │
│  Repository, Logger, Tracer, MetricsRecorder, Clock,              │
│  MessageBroker, ExpressionEngine, AuthorizationProvider,          │
│  WorkerDispatcher                                                 │
└────────────────────────────┬─────────────────────────────────────┘
                             │ flows down into per-instance context
                             v
┌──────────────────────────────────────────────────────────────────┐
│              Instance-level context (RuntimeEnvironment)          │
│   (composed per Process Instance from engine-level extensions +   │
│    instance-scoped state; lifetime = instance lifetime)           │
│                                                                   │
│  Scope (instance-rooted)                                          │
│  InstanceID                                                       │
│  EventProducer (instance-scoped projection of EventHub)           │
│  RenderRegistrator (instance-scoped human-interaction registrator)│
│  (+ engine-level extensions accessible by reference)              │
└──────────────────────────────────────────────────────────────────┘
```

`RuntimeEnvironment` уровня instance — это то, что узлы видят во время исполнения. По двухслойной модели из [ADR-001 v.4](ADR-001-execution-model.md) (Instance + track; token — проекция шага track'а), он передаётся в track'и; track'и читают его для lookup'ов по scope, продукции событий и т.д.

### 4.2 Каталог расширений уровня движка

| Интерфейс | Назначение | Реализация-по-умолчанию | Статус относительно текущего кода |
|---|---|---|---|
| `Repository` | Персистит состояние Process Instance, историю, inbox сообщений, подписки ожидания. Фундамент save/restore по ADR-001 v.4 §4.7 (инварианты рантайма) + Persistence & State ADR. | in-memory (недолговечная) | НОВЫЙ — не существует |
| `Logger` (core-интерфейс; `*slog.Logger` удовлетворяет ему напрямую) | Структурированное логирование | `slog.Default()` — видимый по умолчанию по [памяти проекта](../../) | НОВЫЙ |
| `Tracer` (в форме OTel, определён в ядре — **без импорта OTel**) | Спаны распределённой трассировки | **no-op** (спаны стоят аллокацию на каждое событие, инертны без бэкенда); opt-in в in-memory кольцо последних спанов или `adapters/otel/` | НОВЫЙ |
| `MetricsRecorder` (в форме OTel, определён в ядре — **без импорта OTel**) | Инструменты counter / histogram / gauge | **in-memory запрашиваемый реестр** — видимый по умолчанию, дешёвый, с ограничением числа series, `Snapshot()` для тестов/диагностики; подмена на no-op или `adapters/otel/` | НОВЫЙ |
| `Clock` | Текущее время + sleep (тестируемость таймеров) | wall clock (`time.Now`) | НОВЫЙ |
| `MessageBroker` | Inbox входящих Message; маршрутизация корреляции по [docs/bpmn-spec/semantics/correlation.md](../bpmn-spec/semantics/correlation.md) | in-memory inbox | НОВЫЙ |
| `ExpressionEngine` | Вычисляет `FormalExpression` (BPMN conditionExpression, условия gateway, кардинальность MI и т.д.) | простой Go-нативный evaluator | РАСШИРЯЕТ — `data.FormalExpression` существует; движок оборачивает |
| `AuthorizationProvider` | Авторизует чувствительные операции (старт Process, claim UserTask, отмена Instance, …) | «разрешить всё» | НОВЫЙ |
| `WorkerDispatcher` | Диспетчеризует подходящие Task (ServiceTask / GlobalTask) удалённым воркерам по [SAD-001 §13.2](SAD-001-vision-and-architecture.md) | in-process локальное исполнение | НОВЫЙ |
| `EventHub` | Центральное распределение событий (существующий богатый интерфейс) | in-memory hub (текущая реализация) | **INTERNAL** — execution-обвязка, не точка расширения: нет use-case'а подмены (distribution живёт на границах — `MessageBroker`/`Repository`/`WorkerDispatcher`), а его контракт несёт тонкости внутренней корректности из FIX-001. Остаётся в `internal/eventproc`; будущий ADR может промоутить его, если когда-нибудь понадобится подключаемый hub (internal→public не ломает; обратное — ломает). |
| `TaskDistributor` | Маршрутизация UserTask людям (композит концернов `Renderer` + `Registrator`) | текущая реализация `internal/interactor` | **ОТЛОЖЕНО** — кластер `Interactor`/`Registrator` сцеплен и навязывает выбор слоения model→tasks; промоушн (и переименование `Registrator → TaskDistributor`) принадлежит выделенному **ADR взаимодействия с человеком** (ADR-001 v.4 §9). Не входит в набор контрактов скелета; внутренний кластер остаётся как есть. |

**Принцип проектирования интерфейсов.** Держаться как можно ближе к устоявшемуся индустриальному интерфейсу; обобщать дальше только тогда, когда нет единственного очевидного кандидата.

- **`Logger`** — есть один очевидный Go-стандарт (`log/slog`). Поэтому core-`Logger` — это маленький leveled-интерфейс (`Debug`/`Info`/`Warn`/`Error(msg string, args ...any)`), которому **`*slog.Logger` удовлетворяет напрямую**, так что дефолт — `slog.Default()` без обёртки, а не-slog логгеры тоже можно подключить.
- **`Tracer` / `MetricsRecorder`** — OpenTelemetry — де-факто стандарт, поэтому core-интерфейсы **смоделированы по форме OTel-API** (start/end/attributes/record-error спана; инструменты counter/histogram/gauge). Чтобы сохранить ядро только-stdlib-плюс-`uuid` (SAD-001 G2), ядро **не импортирует** OTel-модули — оно определяет интерфейсы в форме OTel, а реальные OTel-типы живут только в `adapters/otel/` как тонкий pass-through.

**Телеметрия по умолчанию — выбрана по стоимости сигнала (не сплошной no-op).** Сплошной no-op оставляет zero-config движок слепым (против политики наблюдаемости); дефолт на основе логирования превращает метрики в текст лога, который надо распарсивать обратно (мусор в потоке логов). Поэтому дефолты различаются по сигналу:

- **Метрики → in-memory запрашиваемый реестр, включён по умолчанию.** Атомарные counter'ы + gauge'и текущего значения + histogram'ы с фиксированными bucket'ами, читаемые через `Snapshot()`. Инкременты — наносекунды, а footprint ограничен — но он **ограничивает общее число series** (имя counter'а × набор label'ов), отбрасывая-и-предупреждая-однократно за порогом, так что label'ы высокой кардинальности (`process_id`, `instance_id`) не могут заставить его расти неограниченно. Видим по умолчанию, структурирован (не выскребается из логов) и тривиально проверяем в тестах. Подмена на no-op для тишины или на `adapters/otel/` для production.
- **Трассировка → no-op по умолчанию.** Спан стоит аллокацию на каждое событие и инертен без потребляющего бэкенда, так что всегда-включённая трассировка — это ровно тот «мусор, сконфигурированный по умолчанию», которого надо избегать. Ограниченное in-memory **кольцо последних спанов** (последние N, запрашиваемые) поставляется как однострочный opt-in для dev/debug, рядом с реальным `adapters/otel/`.
- **Персистентная (DB) телеметрия** — это production-адаптер (`adapters/sqlstore`), **никогда не core-дефолт** — она добавила бы зависимость от драйвера и росла бы неограниченно; embedder выбирает это хранилище осознанно.

**In-memory дефолты ограничены (cross-cutting принцип).** Cap на число series метрик — это один частный случай правила, управляющего *каждым* in-memory дефолтом: он ограничивает собственный рост и **отбрасывает/вытесняет и предупреждает однократно** за порогом, а не растёт без предела и не падает молча. Видимый-и-ограниченный бьёт и молчаливый, и неограниченный; production подменяет на долговечный/внешний адаптер, который владеет retention. Конкретно:

- `Repository` (in-mem) — вытесняет терминальные Instance и их историю за порогом retention.
- `MessageBroker` (in-mem inbox) — ограничивает inbox / истекает по TTL некоррелированные сообщения.
- `WorkerDispatcher` (in-process) — ограниченный пул воркеров, не неограниченная горутина-на-задачу.

Точный порог и политика вытеснения для каждого решаются в landing-SRD соответствующего расширения (скелет SRD-004 поставляет cap на число series метрик; остальное приземляется со своей execution-обвязкой). **`AuthorizationProvider` — исключение** — его дефолтный вопрос не про рост, а про *security posture*: дефолт «разрешить всё» делегирует авторизацию хост-приложению, с «запретить всё» как opt-in для закрытой системы. Это решается, когда приземляется enforcement авторизации, не здесь.

### 4.3 Интерфейс RuntimeEnvironment — расширен; Instance — это реализация

Существующий `RuntimeEnvironment` в `internal/renv/renv.go` уже структурирован правильно: это интерфейс, и `Instance` — это тип, который его реализует. Track получает ровно одну внешнюю ссылку при конструировании — свой владеющий `*Instance` — и достаёт всё, что ему нужно, через неё.

**Этот ADR просто расширяет существующий интерфейс RuntimeEnvironment новыми сервисами уровня движка.** Без структурного рефакторинга отношения Instance/track; без второй ссылки для track'а; без forwarding-accessor'а.

Есть **два tier'а**: разрешённая конфигурация уровня движка/сервера
(`EngineRuntime`) и контекст исполнения уровня instance
(`RuntimeEnvironment`), который встраивает первый.

```go
// EngineRuntime is the PUBLIC piece, promoted to pkg/ (path per ADR-003). The
// instance-level RuntimeEnvironment below STAYS in internal/renv — it embeds
// internal types — and embeds the public EngineRuntime.

// EngineRuntime — engine/server-level: the Thresher's RESOLVED extension set
// (the wired services). Thresher owns/implements it. Adapters receive it (§3.5);
// RuntimeEnvironment embeds it so tracks keep one uniform call style.
type EngineRuntime interface {
    Logger() Logger                              // core interface; *slog.Logger satisfies it
    Tracer() Tracer
    MetricsRecorder() MetricsRecorder
    Clock() Clock
    Repository() Repository
    ExpressionEngine() expression.Engine         // type is expression.Engine (avoids stutter)
    MessageBroker() MessageBroker
    AuthorizationProvider() AuthorizationProvider
    WorkerDispatcher() WorkerDispatcher
    // NOTE: EventHub is NOT here — it is internal execution plumbing, not an
    // extension point (no substitution use-case; distribution lives at the
    // boundaries: MessageBroker/Repository/WorkerDispatcher). The engine builds
    // its internal hub itself. Likewise human-task interaction (today's
    // instance-level RenderRegistrator, the would-be engine-level
    // TaskDistributor) is deferred to a dedicated human-interaction ADR
    // (ADR-001 v.4 §9); the internal interactor cluster stays as-is.
}

// RuntimeEnvironment — instance-level execution context: engine services
// (embedded) + instance-local state. Instance implements it. It STAYS internal
// (it embeds internal scope.Scope and exposes internal EventProducer /
// RenderRegistrator); only the embedded EngineRuntime is public.
type RuntimeEnvironment interface {
    EngineRuntime                                // engine/server services (embedded, public)
    scope.Scope                                  // data scoping rooted at this instance (internal)
    InstanceID() string                          // instance identity
    EventProducer() EventProducer                // instance-scoped event production (internal)
    RenderRegistrator() interactor.Registrator   // human interaction (internal; promotion deferred)
}
```

Instance получает методы сервисов движка **бесплатно**, встраивая значение
`EngineRuntime` Thresher'а; он лишь добавляет свои instance-локальные методы. Без per-method
делегатов.

```go
// internal/instance/instance.go (existing struct, extended)
type Instance struct {
    renv.EngineRuntime           // embedded: the Thresher's resolved EngineRuntime (public)
    id          string
    scope       scope.Scope
    eventProd   EventProducer
    rr          interactor.Registrator   // human interaction (internal; promotion deferred)
    // ... per ADR-001 v.4
}

// Instance-local — direct (engine-level methods are promoted from the embedded
// EngineRuntime, so Logger()/Repository()/Clock()/... need no code here).
func (i *Instance) InstanceID() string                      { return i.id }
func (i *Instance) Scope() scope.Scope                      { return i.scope }
func (i *Instance) EventProducer() EventProducer            { return i.eventProd }
func (i *Instance) RenderRegistrator() interactor.Registrator { return i.rr }
```

Места вызова из track'а единообразны: одна ссылка, один стиль вызова для всего.

```go
type track struct {
    inst *Instance                       // the ONLY external object track gets at construction
    // ... per ADR-001 v.4
}

// Uniform call style — track doesn't need to know which call is "instance" vs "engine":
t.inst.Scope().GetData(...)              // instance-local — Instance returns its own field
t.inst.ID()                              // instance-local
t.inst.Logger().Info(...)                // engine service — promoted from embedded EngineRuntime
t.inst.Clock().Now()                     // engine service — promoted from embedded EngineRuntime
t.inst.Repository()                      // engine service — promoted from embedded EngineRuntime
```

**Обоснование модели одна-ссылка / Instance-как-RE** (по указанию пользователя):

- Track'у уже нужна ссылка на Instance (для скоупированных по instance концернов вроде `Scope` и `ID`). Добавление ВТОРОЙ ссылки для сервисов движка дублирует обвязку без выигрыша.
- Instance — естественная точка композиции — он знает instance И держит ссылку на конфиг движка.
- У track'а всегда лишь одна внешняя зависимость: его владеющий Instance. Проще для новых контрибьюторов, проще для тестирования, проще в обвязке горутин по ADR-001 v.4.
- «Instance для исполнения, не для хранения рантайм-значений» удовлетворяется композицией: Instance **встраивает `EngineRuntime` Thresher'а** (держателя разрешённых значений); методы уровня движка промоутятся из него, а не переопределяются. Рантайм-значения принадлежат движку (`EngineRuntime`); Instance экспонирует их через встраивание.

Существующий паттерн (Instance реализует RuntimeEnvironment) сохранён; вклад этого ADR в него — лишь расширенный интерфейс (дополнительный набор методов уровня движка).

### 4.4 Паттерн сборки (функциональные опции)

`thresher.New(id, ...Option)` — единственный конструктор. Без опций даёт рабочий движок со всеми дефолтами; каждая опция переопределяет один дефолт.

```go
// Zero-config — defaults applied internally; works out of the box
engine, _ := thresher.New("my-engine-id")

// Custom configuration — options override individual defaults
engine, _ := thresher.New("my-engine-id",
    thresher.WithRepository(postgresRepo),
    thresher.WithLogger(slog.New(otelHandler)),
    thresher.WithTracer(otelTracer),
    thresher.WithMetricsRecorder(prometheusRecorder),
    thresher.WithClock(realClock),
    thresher.WithMessageBroker(redisBroker),
    thresher.WithAuthorizationProvider(authz),
    thresher.WithWorkerDispatcher(grpcDispatcher),
)
```

Каждый `WithXxx` — это `thresher.Option`, возвращающий замыкание, которое мутирует внутреннюю config-структуру во время `New()`. Опции НЕ имеют зависимости от порядка, если это явно не задокументировано; если `WithXxx` встречается несколько раз для одного и того же расширения, побеждает последний (last-write семантика; стандартная конвенция функциональных опций).

Внутри `New` инициализирует конфиг значениями по умолчанию, применяет каждую переданную опцию по порядку, затем логирует разрешённую конфигурацию:

```go
func New(id string, opts ...Option) (*Thresher, error) {
    cfg := defaultConfig()           // ALL defaults wired here
    for _, opt := range opts {
        opt(&cfg)                    // override per option
    }
    t, err := assemble(id, cfg)
    if err != nil {
        return nil, err
    }
    t.logStartupConfig()             // INFO line — see §4.4.1
    return t, nil
}
```

Этот паттерн означает, что «дефолт» — внутренняя деталь реализации `New`, а не обращённый к пользователю альтернативный конструктор. Публичный API — одна функция.

#### 4.4.1 Логирование стартовой конфигурации

После того как `New` завершает связывание, Thresher эмитит одну лог-строку уровня INFO через сконфигурированный `Logger`, суммируя разрешённое связывание расширений. Это даёт ops однострочный ответ на вопрос «с чем сконфигурирован этот движок?» в момент конструирования.

Формат (иллюстративный — финальная структура фиксируется при реализации):

```
INFO thresher.starting
     id=my-engine
     repository=*memrepo.Repository
     logger=*slog.Logger(JSONHandler)
     tracer=noop.Tracer
     metricsRecorder=noop.MetricsRecorder
     clock=*systemclock.Clock
     messageBroker=*membroker.Broker
     expressionEngine=*goexpr.Engine
     authorizationProvider=*allowall.Provider
     workerDispatcher=*inproc.Dispatcher
```

Каждое значение — это Go-тип связанной реализации. Лог-строка структурирована (slog-атрибуты), а не вольная проза — нижестоящие процессоры логов могут pivot'иться по индивидуальным типам расширений.

Поведение:

- Уровень INFO по умолчанию. Строка молчит только в том случае, если пользователь явно сконфигурирует Logger, отбрасывающий INFO-вывод. Это согласуется с политикой наблюдаемости проекта «видимо-по-умолчанию» ([память: политика наблюдаемости](../../)).
- Эмитится ровно один раз на вызов `New`, после применения опций, но до того, как движок начнёт принимать работу.
- Обязательна, не опциональна. Нет опции `WithoutStartupConfigLog()` — заглушить её — ответственность пользователя через конфигурацию его Logger.

### 4.5 Политика реализаций-по-умолчанию

Каждый интерфейс уровня движка имеет дефолт, который:

- **Logger**: `slog.Default()` (видим по умолчанию по политике проекта — случайная тишина хуже случайного шума).
- **Tracer, MetricsRecorder, AuthorizationProvider**: no-op. «Видимо-по-умолчанию» не применяется, потому что Go stdlib не имеет разумного дефолта для них; пользователи делают opt-in через адаптеры.
- **Repository, MessageBroker**: in-memory, недолговечные. Подходят для тестов / встроенного недолгоживущего использования; production подменяет через адаптер.
- **Clock**: wall clock. Тесты внедряют поддельный clock для зависящего от времени поведения BPMN (Timer-события).
- **WorkerDispatcher**: in-process локальное исполнение. Позиция «distribution через opt-in» из SAD-001 §13.
- **ExpressionEngine**: минимальный Go-нативный evaluator, поддерживающий простые выражения; пользователи подключают JUEL / FEEL / и т.д. через адаптер.
- **EventHub**: остаётся internal (`internal/eventproc/eventhub`) — execution-обвязка, не публичное расширение (см. §4.2).
- **TaskDistributor**: отложен — кластер `internal/interactor` остаётся internal до ADR взаимодействия с человеком (см. §4.2).

Дефолты упакованы в ядро. Adapter-модули (`adapters/*`) предоставляют production-реализации.

### 4.6 Конвенции adapter-модулей

По multi-module монорепо SAD-001 §9.2:

- Каждый адаптер — собственный Go-модуль: `github.com/dr-dobermann/gobpm/adapters/<name>` со своим `go.mod`.
- Адаптер ОБЯЗАН реализовывать один или более публичных интерфейсов расширений из ядра (`pkg/`).
- Адаптер НЕ ДОЛЖЕН импортировать пакет любого другого адаптера. Это правило **no-cross-adapter-imports**.
- Адаптер МОЖЕТ брать свои зависимости по умолчанию из внедрённого `EngineRuntime` (§3.5 Паттерн C / §8.3 `RuntimeAware`) и/или принимать явные опции на уровне адаптера для их переопределения (split). В любом случае он зависит только от core-интерфейсов (`EngineRuntime`, `Repository`, …), никогда — от другого адаптера или конкретного движка — правило no-cross-imports соблюдается.
- Адаптер ДОЛЖЕН (SHOULD) декларировать свою минимальную совместимую версию ядра через `replace`-free пиннинг в своём `go.mod`.
- Тесты адаптера ДОЛЖНЫ (SHOULD) проверять против контракта, опубликованного в этом ADR (например, реализация `Repository` должна проходить тот же conformance-набор тестов, что проходит in-memory дефолт).
- Адаптеры ОБЯЗАНЫ предпочитать **pure-Go embedded** реализации сервис-зависимым, чтобы сохранить ценностное предложение embeddable-библиотеки у ядра. Сервис-зависимые адаптеры (gRPC sidecar'ы, внешние HTTP-сервисы) разрешены, но ДОЛЖНЫ (SHOULD) быть чётко помечены как таковые.

Начальные цели для адаптеров (иллюстративные; не написаны в v.1 этого ADR):

- `adapters/postgres/` → `Repository` (pure-Go через `lib/pq` или `pgx`)
- `adapters/otel/` → `Tracer`, `MetricsRecorder` (pure-Go OpenTelemetry SDK)
- `adapters/oidc/` → identity-claims (питает контекст `AuthorizationProvider`)
- `adapters/casbin/` → `AuthorizationProvider` (casbin — pure-Go in-process по умолчанию; сервисный режим — opt-in и не рекомендуемый путь для embedded-использования)
- Альтернатива Simple-RBAC → `AuthorizationProvider` (меньший embedded-вариант для проектов, которым не нужен полный язык политик casbin)
- `adapters/redis-broker/` → `MessageBroker` (сервис-зависимый — пометили бы как таковой)
- `adapters/nats-broker/` → `MessageBroker` (сервис-зависимый)
- `adapters/feel/` → `ExpressionEngine` (FEEL-evaluator, pure-Go)

Выбор AuthZ-адаптера заслуживает краткой заметки: **casbin** на самом деле pure-Go и работает in-process по умолчанию; режим «casbin как сервис» — opt-in вариант развёртывания, не единственный путь. Так что `adapters/casbin/` вписывается в предпочтение «pure-Go embedded». Меньшие альтернативы (`gorbac`, кастомный RBAC, embedded OPA) — равно валидные выборы и должны быть доступны — точка расширения AuthZ — это интерфейс, а не какая-то конкретная реализация.

### 4.7 Стабильность и версионирование

Как только этот ADR Принят и интерфейсы опубликованы в `pkg/`:

- Каждый публичный интерфейс расширения — **semver-стабильный контракт**. Мажорная версия core-модуля `gobpm` выражает стабильность интерфейсов.
- **Обратно-совместимые добавления** (новые методы на новом интерфейсе, новые опции) — MINOR-bump версии.
- **Ломающие изменения** (изменение сигнатур методов существующего интерфейса, удаление расширения) требуют нового ADR (или изменённой версии этого) И MAJOR-bump версии.
- Адаптеры декларируют диапазон совместимых версий ядра; несовпадение мажорной версии — ошибка на этапе компиляции.

До-1.0 (где мы сейчас): эволюция интерфейсов свободнее по semver-конвенции Go. Дисциплина выше включается на v1.0.0.

## 5. Концепция против текущего кода — намеренные отклонения

| Тема | Текущий код | Этот ADR | Требуемое изменение |
|---|---|---|---|
| Конструктор движка | `Thresher.New(id string)` — без опций | `Thresher.New(id, opts ...Option)` — единственный конструктор; без опций применяет все дефолты; каждая опция переопределяет один дефолт. Нет `NewDefault`. | Добавить тип `Option`. Добавить реализации функциональных опций для каждого расширения уровня движка. Отрефакторить `New`, чтобы он инициализировал дефолты внутренне, затем применял опции. |
| Разделение EngineRuntime / RuntimeEnvironment | `RuntimeEnvironment` (internal) со встраиванием `scope.Scope`, `InstanceID()`, `EventProducer()`, `RenderRegistrator()` | **Выделить `EngineRuntime`** (accessor'ы расширений уровня движка: `Logger`, `Tracer`, `MetricsRecorder`, `Clock`, `Repository`, `ExpressionEngine`, `MessageBroker`, `AuthorizationProvider`, `WorkerDispatcher`) → **public** (`pkg/`, путь по ADR-003), реализуемый `Thresher`. `RuntimeEnvironment` **остаётся internal**, встраивает публичный `EngineRuntime` и сохраняет `scope.Scope`/`InstanceID()`/`EventProducer()`/`RenderRegistrator()` (все internal-типизированные). `EventHub`/`EventProducer` и взаимодействие с человеком остаются internal. | Промоутить только `EngineRuntime` в `pkg/`; оставить `RuntimeEnvironment` в `internal/renv`. |
| Instance-как-реализация-RE | Уже сделано в текущем коде | Сохранено; track видит только `*Instance` и вызывает единообразный набор методов | Без изменения отношений — Instance продолжает реализовывать (расширенный) интерфейс RuntimeEnvironment. Добавить новые методы-делегаты уровня движка в Instance, каждый форвардит в конфиг движка. |
| Стартовое логирование Thresher | Нет стартового логирования | Thresher эмитит одну структурированную лог-строку уровня INFO, суммирующую разрешённое связывание расширений, на каждый успешный вызов `New` (§4.4.1) | Добавить метод `logStartupConfig` в Thresher. Обязательное поведение; нельзя отказаться (пользователь заглушает через конфиг своего Logger при желании). |
| Интерфейс Repository | Не существует | Определить `Repository` в `pkg/` с методами checkpoint / load / list-in-flight по ADR-001 v.4 §4.7 + Persistence & State ADR | Реализовать in-memory дефолт. Добавить в конфиг `Thresher`. |
| Logger / Tracer / MetricsRecorder | Не существуют | `Logger` = slog-удовлетворяемый core-интерфейс; `Tracer`/`MetricsRecorder` = core-интерфейсы в форме OTel (без импорта OTel — SAD-001 G2) | Дефолты: `slog.Default()` Logger; **in-memory запрашиваемый реестр** Metrics (с cap на series, `Snapshot()`); **no-op** Tracer (in-mem кольцо спанов + OTel — opt-in). Реальный OTel в `adapters/otel/`. |
| Clock | Не существует | Определить интерфейс `Clock` в `pkg/` | Реализовать wall-clock дефолт. Внедрить в обработку Timer-событий. |
| MessageBroker | Не существует | Определить в `pkg/` по [bpmn-spec/semantics/correlation.md](../bpmn-spec/semantics/correlation.md) | Реализовать in-memory inbox дефолт. |
| AuthorizationProvider | Не существует | Определить в `pkg/`; точки-хуки на чувствительных операциях | Реализовать allow-all дефолт. Идентифицировать места вызова hook-point'ов. |
| WorkerDispatcher | Не существует | Определить в `pkg/` по [SAD-001 §13.2](SAD-001-vision-and-architecture.md) | Реализовать in-process dispatch дефолт. |
| ExpressionEngine | Частично: `data.FormalExpression` в `pkg/model/data/` | Обернуть вычисление `FormalExpression` в интерфейс `ExpressionEngine` на уровне `pkg/` | Промоутить / добавить ExpressionEngine. Дефолт использует существующий Go-evaluator. |
| EventHub | `internal/eventproc.EventHub` (внешне нереализуемый) | **Остаётся internal** — execution-обвязка, не точка расширения (§4.2) | Без изменений. |
| TaskDistributor / RenderRegistrator | `internal/interactor.Registrator` + экосистема `Renderer` | **Отложено** в выделенный ADR взаимодействия с человеком (ADR-001 v.4 §9) — кластер сцеплен и навязывает выбор слоения model→tasks | Без изменений в скелете; внутренний кластер и `RenderRegistrator()` остаются как есть. |
| Расположение RuntimeEnvironment | `internal/renv` | Перенести в `pkg/` (вероятно `pkg/renv`) | Перенести. Обновить Instance + track, чтобы импортировать из нового расположения. |
| Документация расширений | Разбросана по комментариям в коде | Единый канонический каталог расширений в этом ADR | Поддерживать этот ADR как источник истины каталога. |

**Как это приземляется (разрешает порядок с §7).** Отклонения реализуются вместе — только в **минимальном / дефолтном поведении** — в **одном фундаментальном SRD** («скелет расширений»): каждый интерфейс определён в `pkg/`, каждый со своей упакованной реализацией-по-умолчанию, сборка через функциональные опции, расширенный `RuntimeEnvironment` и стартовая лог-строка, связанные так, чтобы движок работал end-to-end на сегодняшней поддержке BPMN. **Этот ADR переходит из Draft в Accepted, когда тот единственный SRD приземляется и его тесты из §7 проходят** (не раньше — см. §7). Production-адаптеры и глубина на каждый интерфейс (долговечный Repository, реальный MessageBroker, FEEL ExpressionEngine, удалённый WorkerDispatcher, …) следуют как более поздние, отдельно гейтируемые SRD. Точные пути пакетов (где в `pkg/` живёт каждый интерфейс) отложены в ADR-003 Module Layout.

## 6. Последствия

### 6.1 Плюсы

- **Usability «из коробки» сохранён.** `thresher.New(id)` без опций даёт рабочий движок одним вызовом (дефолты и есть дефолты; нет отдельного `NewDefault`).
- **Матрица расширений задокументирована.** Будущие контрибьюторы и авторы адаптеров имеют единый источник истины о том, что подключаемо.
- **Go-идиоматично.** Никаких фреймворков, никаких DI-контейнеров, никаких загрузчиков плагинов — только интерфейсы + функциональные опции.
- **Стабильная public-поверхность.** Публичные интерфейсы в `pkg/` несут semver-контракт; внутренняя реализация может свободно эволюционировать.
- **Экосистема адаптеров включена.** Чёткий контракт на то, что поставляют adapter-модули и как они декларируют совместимость.
- **Наблюдаемость видима-по-умолчанию.** Согласуется с политикой проекта; случайная тишина избегается.

### 6.2 Минусы

- **Больше public-поверхности для сопровождения.** 10+ интерфейсов расширений становятся контрактами стабильности на v1.0. Каждое изменение интерфейса требует осторожности.
- **Реализации-по-умолчанию, упакованные в ядро, раздувают поверхность core-модуля.** Смягчается тем, что дефолты держатся маленькими и хорошо изолированными (один файл на дефолт).
- **Именование интерфейсов будет следовать индустриальным конвенциям.** Некоторые имена несут исторический багаж (`Registrator`, а также `WorkerDispatcher` / `TaskDistributor` из SAD против альтернатив вроде `ServiceTaskExecutor`). Переименование интерфейсов расширений к устоявшемуся индустриальному словарю на фазе реализации **принято и ожидаемо** — они не заморожены как контракты, пока этот ADR не Принят. **Исключение — конкретный тип движка остаётся `Thresher`:** он намеренно характерен; `Server` и `Engine` слишком обобщённые, чтобы именовать тип, и зарезервированы как *ролевые* слова (например, интерфейс `EngineRuntime` уровня движка — «рантайм-контракт движка»), никогда — собственное имя типа.
- **Некоторые текущие internal-хелперы переезжают в `pkg/`** — став публичными, их нельзя отрефакторить без изменения ADR.

### 6.3 Влияние на смежные решения

- **ADR-003 Module Layout**: определяет, где именно живёт каждый интерфейс в дереве подкаталогов `pkg/`. Вероятные кандидаты: `pkg/extension/` (единый подпакет) или один подпакет на концерн (`pkg/persistence`, `pkg/observability`, `pkg/auth`, `pkg/expression` и т.д.).
- **ADR-004 Runtime Environment Contract**: слой рантайма связывает production-grade адаптеры (postgres + otel + oidc + casbin + …) в движок через эти опции расширений.
- **ADR-001 Execution Model (v.3)**: `Repository` — это интерфейс персистентности, на который нацелены инварианты рантайма из ADR-001 v.4 §4.7 (и Persistence & State ADR). `Logger` / `Tracer` / `MetricsRecorder` потребляют рантайм-поток событий instance — единый поток `trackEvent` и выраженное в токенах представление, выведенное из него (ADR-001 v.4 §4.3).

## 7. Verification

Как мы узнаем, что архитектура расширений работает:

| Что | Как |
|---|---|
| **New без опций работает end-to-end** | Интеграционный тест: `thresher.New("test").Run(ctx)`; зарегистрировать процесс; стартовать instance; проверить, что он завершается. Дефолты связаны внутренне; нулевая конфигурация со стороны пользователя. |
| **Функциональные опции компонуются без проблем порядка** | Тест: сконструировать движок со всеми 10 опциями `WithXxx` в случайном порядке; убедиться, что результирующее состояние движка идентично. |
| **Каждая опция переопределяет дефолт** | Тест на каждую опцию: сконструировать с `WithLogger(custom)`; убедиться, что Logger движка — это `custom`, а не `slog.Default()`. Повторить для каждого интерфейса. |
| **Last-write семантика** | Тест: передать `WithLogger(A), WithLogger(B)`; убедиться, что движок использует B. |
| **Кросс-адаптерная композиция** | Тест (с реальным/поддельным `RuntimeAware`-адаптером): без опции хранилища AuthZ-адаптер использует `Repository` движка через внедрённый `EngineRuntime` (дефолтное разделение); с `WithStorage(otherRepo)` он использует вместо этого его (split). Проверяет §3.5 Паттерн C / §8.3. |
| **Стартовая лог-строка конфигурации** | Тест: сконструировать движок с Logger, который захватывает записи. Убедиться: эмитится ровно одна запись уровня INFO с ключом `thresher.starting`, содержащая атрибуты для каждого расширения уровня движка (`repository`, `logger`, `tracer`, `metricsRecorder`, `clock`, `messageBroker`, `expressionEngine`, `authorizationProvider`, `workerDispatcher`) со значениями, совпадающими с именами типов связанных реализаций. Проверяет §4.4.1. |
| **Instance реализует RuntimeEnvironment** | Тест на type assertion: `var _ RuntimeEnvironment = (*Instance)(nil)`. Проверка на этапе компиляции, что Instance удовлетворяет расширенному интерфейсу. |
| **Делегаты сервисов движка на Instance** | Тест на каждый метод: сконструировать движок с кастомным Logger (или Clock, или Repository, …); породить Instance; убедиться, что `instance.Logger()` (и т.д.) возвращает то же значение, что держит конфиг движка. Проверяет корректность однострочных делегатов. |
| **Реализации-по-умолчанию соответствуют контракту публичного интерфейса** | Conformance-тест: in-memory Repository дефолт проходит тот же conformance-набор, что прошёл бы гипотетический postgres Repository. То же для MessageBroker и т.д. |
| **Движок без опционального расширения всё равно работает** | Smoke-тест: опустить каждый опциональный `WithXxx`; убедиться, что движок `New(id)` без опций работает. |
| **Изоляция adapter-модуля** | Когда adapter-модули существуют: импорт только `core` НЕ протягивает транзитивно зависимости `adapters/*`. Проверяется через `go mod graph`. |
| **Композиция RuntimeEnvironment корректна** | Тест: породить Instance; убедиться, что его `RuntimeEnvironment.Logger()` — это Logger движка; убедиться, что `Scope` укоренён в instance; убедиться, что `EventProducer` скоупирован по instance. |

**Гейт приёмки** (Draft → Accepted): эти тесты ОБЯЗАНЫ существовать и проходить против **фундаментального SRD скелета расширений** (§5) — реализации только-дефолты. Две **зависящие-от-адаптера** строки (*кросс-адаптерная композиция*, *изоляция adapter-модуля*) могут запуститься только тогда, когда существует реальный adapter-модуль; они отложены до SRD первого адаптера и **не** требуются для приёмки этого ADR. Пока SRD скелета не приземлился и его применимые тесты не прошли, ADR остаётся в Draft.

## 8. Рекомендации по enterprise-готовности

Эта секция фиксирует cross-cutting лучшие практики для авторов адаптеров и production-развёртываний. Они рекомендательные — не нормативные — но каждая закрывает класс операционного отказа, наблюдаемый в реальных развёртываниях BPM-движков. Рекомендации написаны в предположении, что проект движется из research-фазы в production-фазу использования.

### 8.1 Конвенции наблюдаемости

Консистентный словарь наблюдаемости через Logger, Tracer и MetricsRecorder — это разница между «диагностировать за минуты» и «диагностировать за дни». Стандартизируйте три вещи:

**Ключи атрибутов Logger** (slog-конвенции; стабильные, документированные имена):

| Атрибут | Всегда присутствует в | Пример значения |
|---|---|---|
| `gobpm.engine_id` | Всех записях, эмитированных движком | `"my-engine"` |
| `gobpm.instance_id` | Записях, скоупированных по instance | UUID |
| `gobpm.process_id` | Записях, скоупированных по instance | `"order-fulfillment"` |
| `gobpm.track_id` | Записях, скоупированных по track | UUID |
| `gobpm.token_id` | Записях, связанных с token | UUID |
| `gobpm.node_id` | Записях исполнения узла | `"ServiceTask_ChargeCard"` |
| `gobpm.element_type` | Записях исполнения узла | `"ServiceTask"` |
| `trace_id`, `span_id` | Когда Tracer связан (OTel-совместимо) | hex-строки |
| `tenant_id` | Когда tenancy связан (по ADR-004) | идентификатор тенанта |

Эти ключи появляются в production-логах с первого дня. Пропуск их при разработке в research-фазе создаёт слепые зоны, бьющие сильно во время первого реального production-инцидента.

**О выборе стандарта трассировки.** OpenTelemetry — единственный жизнеспособный открытый стандарт распределённой трассировки на данный момент. Предшественники (OpenTracing, OpenCensus) слились в OTel; вендор-специфичные SDK (Datadog APM, агент New Relic Go) несут lock-in. Мы определяем собственный интерфейс `Tracer` в `pkg/` (по §4.2), а не реэкспортируем типы OTel напрямую — это сохраняет свободу подменить бэкенд трассировки, если ландшафт изменится, и держит `core` свободным от зависимостей по SAD-001 G2. Дефолтный `Tracer`-адаптер оборачивает OTel; пользователи, которым нужен другой бэкенд, пишут собственный адаптер. Сигнатуры методов интерфейса `Tracer` ДОЛЖНЫ (SHOULD) зеркалить словарь спанов OTel (start span, set attributes, record error, end), чтобы минимизировать impedance-mismatch.

**Иерархия спанов Tracer** (отображает дерево исполнения BPMN в спаны в стиле OTel):

```
thresher.engine.run
  └─ thresher.instance.run        (per Process Instance)
       └─ thresher.track.execute  (per track per ADR-001 v.4)
            └─ thresher.step      (per node visited)
                 └─ child spans   (HTTP / DB / etc. — user code)
```

Каждый спан ДОЛЖЕН (SHOULD) нести те же ключи атрибутов, что и атрибуты logger из §8.1. Стандартные семантические конвенции OTel `process.*`, `db.*`, `http.*` применяются для внешних вызовов внутри узла. Статус спана: ERROR при отказе track'а; восстановление через прерывающее boundary-событие сбрасывает в OK с audit-trail, отмечающим исходную ошибку.

**Именование метрик** (Prometheus / OTel-выровненное, префикс `gobpm_*`):

| Метрика | Тип | Label'ы |
|---|---|---|
| `gobpm_instances_active` | Gauge | `engine_id`, `process_id` |
| `gobpm_instances_completed_total` | Counter | `engine_id`, `process_id`, `outcome` (`normal` / `terminated` / `failed`) |
| `gobpm_tokens_active` | Gauge | `engine_id`, `process_id` |
| `gobpm_track_duration_seconds` | Histogram | `engine_id`, `process_id`, `element_type` |
| `gobpm_repository_op_duration_seconds` | Histogram | `op` (`checkpoint` / `load` / `list_inflight`) |
| `gobpm_message_correlation_attempts_total` | Counter | `outcome` (`matched` / `no_match` / `ambiguous`) |
| `gobpm_authz_decisions_total` | Counter | `outcome` (`allow` / `deny` / `error`) |

Адаптеры МОГУТ регистрировать собственные метрики под суб-пространством имён своего адаптера (например, `gobpm_postgres_connection_pool_busy`) через тот же `MetricsRecorder`.

### 8.2 Операционные ожидания от адаптеров

Production-grade адаптеры несут операционное бремя, которого нет у in-memory дефолтов. Документирование этих ожиданий заранее предотвращает ловушку «интеграционные тесты проходят, но в проде падает».

**Repository:**
- Connection pooling. Connect-disconnect на каждый вызов умирает под нагрузкой.
- Таймауты на каждую операцию. Зависшая БД НЕ ДОЛЖНА блокировать движок неограниченно. Таймаут и проброс ошибки в error-путь движка.
- Идемпотентные операции checkpoint. Повторный прогон checkpoint для одного `(instance_id, state_version)` ОБЯЗАН давать то же персистированное состояние.
- Инструментарий миграции схемы. Адаптер ДОЛЖЕН (SHOULD) поставлять явный `Migrate(ctx)`, а не авто-применять при старте — production-развёртывания часто хотят ручной контроль.
- Экспозиция метрики здоровья пула. Операторам нужно видеть исчерпание пула, прежде чем оно станет outage.

**MessageBroker:**
- Семантика доставки ОБЯЗАНА быть задокументирована. «At-least-once» — это пол; «exactly-once» требует документированного механизма дедупликации.
- Сопоставление по корреляции — работа движка; адаптер НЕ ДОЛЖЕН делать фильтрацию на уровне корреляции.
- Прибытия не-по-порядку ОБЯЗАНЫ толерироваться движком. Адаптеры МОГУТ enforce'ить порядок для конкретных паттернов, но НЕ ДОЛЖНЫ предполагать, что движок полагается на него.
- Dead-letter маршрутизация для некоррелируемых сообщений — production-адаптеры ДОЛЖНЫ (SHOULD) предоставлять; дефолтный in-memory МОЖЕТ опустить.

**AuthorizationProvider:**
- Кэширование решений с коротким TTL (~60с типично). Политики меняются не на каждый запрос.
- API сброса кэша для сценариев изменения политики.
- Fail-closed при ошибке адаптера (deny, не allow). Документировать явно.
- Метрика решений (`gobpm_authz_decisions_total{outcome}`) для видимости ops.

**WorkerDispatcher:**
- Отслеживание heartbeat / liveness воркеров. Отказавший воркер → re-dispatch на здоровый.
- Маршрутизация по capability — воркер регистрирует, что он может исполнять; диспетчер сопоставляет.
- Per-task таймаут, enforce'имый диспетчером, а не движком — движок не знает SLA удалённых воркеров.

### 8.3 Опциональные интерфейсы побочных возможностей

Некоторым адаптерам нужна интеграция жизненного цикла с движком. Вместо перегрузки core-интерфейсов расширений определите опциональные интерфейсы, которые движок детектит через type assertion:

```go
// pkg/extension (or wherever the contracts live)

// Optional — adapters that need explicit startup
type Starter interface {
    Start(ctx context.Context) error
}

// Optional — adapters that need explicit shutdown
type Stopper interface {
    Stop(ctx context.Context) error
}

// Optional — adapters that expose health
type HealthChecker interface {
    HealthCheck(ctx context.Context) error
}

// Optional — adapters that want the engine's resolved services. The engine
// injects its EngineRuntime (§4.3) at New; the adapter uses it to default any
// dependency it wasn't explicitly configured with (e.g. rt.Repository()).
// See §3.5 Pattern C.
type RuntimeAware interface {
    UseRuntime(rt EngineRuntime)
}

// Optional — adapters that declare their cluster-mode compatibility
type ClusterAware interface {
    // ClusterCompatibility returns whether this adapter is safe to use when
    // the runtime is configured in cluster mode. On false, reason explains
    // why (e.g., "in-memory; state not shared across nodes"). The runtime
    // refuses to start in cluster mode if any wired adapter declares (false, _).
    // Adapters that don't implement this interface get a startup warning in
    // cluster mode (compatibility undeclared); they're not blocked.
    ClusterCompatibility() (compatible bool, reason string)
}
```

Когда Thresher конструируется и работает, он детектит, реализует ли каждое зарегистрированное расширение один из этих, и интегрирует соответственно:
- `UseRuntime` вызывается во время `New`, после того как движок разрешает свой конфиг, на каждом связанном адаптере, который его реализует — передавая `EngineRuntime` движка, чтобы адаптер мог взять свои зависимости по умолчанию из движка (§3.5 Паттерн C).
- `Start` вызывается во время setup'а `Run` до того, как инстансы принимаются.
- `Stop` вызывается во время остановки движка после того, как все инстансы дренированы или завершены.
- `HealthCheck` экспонируется слоем рантайма (по ADR-004) для liveness/readiness эндпоинтов.
- `ClusterCompatibility` запрашивается слоем рантайма при старте, когда активен cluster-режим; любой возврат `(false, reason)` — жёсткий отказ старта. (Содержательный дизайн кластера живёт в будущем ADR-008; по [SAD-001 §13.5](SAD-001-vision-and-architecture.md).)

Адаптеры, которые их не реализуют, просто работают. Это progressive enhancement — маленькие адаптеры остаются простыми; большие адаптеры получают хуки жизненного цикла, когда они им нужны.

### 8.4 Contract-тестирование адаптеров

Единственный самый эффективный инструмент для удержания реализаций адаптеров честными: **каждый адаптер проходит тот же conformance-набор тестов, что и in-memory дефолт.**

Это стандартный Go-паттерн тестирования — без нового фреймворка, без специальной инфраструктуры. Каждый публичный интерфейс расширения поставляется вместе с опубликованным conformance-хелпером: обычной экспортированной функцией, которая принимает `*testing.T` и фабричную функцию для тестируемой реализации, затем прогоняет контракт через subtest'ы. Тесты адаптера — однострочники, вызывающие хелпер.

```go
// Core publishes a conformance helper alongside each extension interface.
// (Exact package location per ADR-003 Module Layout — uses standard Go
// testing.T + table-driven subtests; no special framework.)
func RepositoryConformance(t *testing.T, factory func() Repository) {
    // covers the full Repository contract:
    // checkpoint, load, list-in-flight, idempotency, concurrent access,
    // error paths, large-payload handling, …
}

// Adapter test code is trivial:
func TestPostgresRepository(t *testing.T) {
    RepositoryConformance(t, func() Repository {
        return postgres.NewRepository(testConnString)
    })
}
```

Устоявшиеся Go-проекты используют ровно этот паттерн (`database/sql/driver/driverstest`, `golang.org/x/oauth2/internal/tokenstest` и т.д.). Стандартная идиома, без изобретения велосипеда. Conformance-хелпер — это просто экспортированный тестовый код.

Каждый мажорный тип расширения ДОЛЖЕН (SHOULD) иметь опубликованный conformance-хелпер: `RepositoryConformance`, `MessageBrokerConformance`, `LoggerConformance`, `ClockConformance` и т.д. Точное расположение пакета отложено в ADR-003 Module Layout — но он живёт в test-импортируемом расположении (вероятно подпакет `*_test_helpers` или аналогичное Go-идиоматичное разделение).

### 8.5 Разделение audit- и ops-событий

Два различных концерна выводятся из **единого** in-memory рантайм-потока событий (ADR-001 v.4 §4.3 — `trackEvent`, track → loop; нет второго live-канала). Это два *представления* этого потока, а не два канала:

| Концерн | Представление / источник | Долговечность | Примеры |
|---|---|---|---|
| **Audit-события** — комплаенс, не-должны-теряться | **BPMN-наблюдаемое, выраженное в токенах представление**, выведенное из потока (split / merged / waiting / consumed / withdrawn — ADR-001 v.4 §4.3) | Долговечны; требуется персистентный подписчик | «Пользователь X сделал claim UserTask Y», «Процесс стартован Z», «Авторизация отклонена: user=A action=cancel resource=instance/123» |
| **Ops-события** — диагностика, потеря-приемлема | **сырые переходы track/step** (`trackEvent` + конечный автомат track'а — ADR-001 v.4 §4.2/§4.3) | Best-effort; подписчики Logger/Tracer/MetricsRecorder | «track вошёл в TrackExecutingStep», «StepPrologued завершён», «fork породил новый track» |

Audit-подписчики ДОЛЖНЫ (SHOULD) использовать долговечный транспорт (DB-запись на событие, Kafka с ack и т.д.). Ops-подписчики МОГУТ использовать best-effort транспорт (in-memory каналы, UDP, fire-and-forget).

Смешивание двух (audit-поля включены в ops-логи; ops-шум включён в audit-trail) создаёт комплаенс-трение (аудиторы не хотят ops-шума) и ops-трение (audit-канал становится слишком тихим, чтобы по нему диагностировать).

**Заметка (переоснована на двухслойной модели).** Ранний драфт ADR-002 отображал «TokenEvent = audit / TrackEvent = ops» на тогда-предварительную трёхслойную модель ADR-001; ADR-001 v.4 свернул до двух слоёв (token — проекция, не хранимый объект / отдельный канал событий). Поэтому разделение переформулировано выше как **два выведенных представления одного потока `trackEvent`** — audit = выраженная в токенах проекция, ops = сырые переходы track/step. Различия withdrawn/merged и любые будущие комплаенс-релевантные состояния токена (например, «claimed», «delegated», «escalated») производятся ADR'ами gateway/events ([ADR-005](ADR-005-gateways-and-joins.md)/[ADR-006](ADR-006-events-and-subscriptions.md)); audit-представление расширяется по мере их приземления. Граница предварительна, не зафиксирована.

### 8.6 Обратная совместимость, депрекация и чувствительные данные

**Дисциплина обратной совместимости** (после-1.0; ослаблена до-1.0):

- **Только-добавление изменений** публичных интерфейсов — новые методы на новых интерфейсах, новые опции. Minor-bump версии.
- **Стабильность поведения** — изменение того, что существующий метод возвращает на существующих входах, — это ломающее изменение, даже если сигнатура неизменна.
- **Путь депрекации** — вместо удаления метода пометить `// Deprecated:` с версией удаления. Держать депрецированный метод как минимум одну минорную версию после введения замены.
- **Согласование версий адаптеров** — адаптеры декларируют min-compatible-core через build-теги или ограничения `go.mod`; рантайм детектит несовпадение и отказывается стартовать.

Запекайте дисциплину рано — дооснащать её после жалобы реального пользователя сложнее, чем начать с неё.

**Обработка чувствительных данных**:

Переменные BPMN-процесса могут нести PII / регулируемые данные. Движок сам не классифицирует; классификация, редакция, шифрование — ответственности адаптера.

- `Logger`-адаптеры ДОЛЖНЫ (SHOULD) поддерживать политику редакции на уровне полей (например, `gobpm.process_variable.customer_email` редактируется на INFO; полностью на DEBUG с требуемым разрешением вызывающего).
- `Repository`-адаптеры ДОЛЖНЫ (SHOULD) поддерживать encryption-at-rest для колонки переменных / эквивалентного хранилища.
- Audit-подписчики ДОЛЖНЫ (SHOULD) поддерживать неизменяемый append-only режим для комплаенс-контекстов (SOC2, GDPR, HIPAA).
- Выраженное в токенах audit-представление (выведенное из потока `trackEvent` — §8.5) — естественный audit-фид; движку не нужно знать, какие поля чувствительны — audit-адаптер применяет собственную классификацию по организационной политике.

Это разделение позволяет одному рантайму обслуживать и развёртывания «классификация не нужна» (внутренняя автоматизация), и «требуется строгая классификация» (регулируемые приложения, обращённые к клиенту) без изменений на уровне движка.

## 9. Ссылки

- [SAD-001 Vision & Architecture](SAD-001-vision-and-architecture.md) — §11 Extension Model (этот ADR уточняет); §6 Quality Attributes; §13 Distribution & Scale (предварительно)
- [ADR-001 v.4 Execution Model](ADR-001-execution-model.md) — рантайм, который это расширяет: §4.7 инварианты рантайма, которые персистит Repository (+ Persistence & State ADR для долговечного checkpoint/rehydrate); §4.3 единый поток `trackEvent` + выведенное выраженное в токенах представление, которое потребляют Logger / Tracer / MetricsRecorder / audit-подписчики
- [docs/bpmn-spec/semantics/correlation.md](../bpmn-spec/semantics/correlation.md) — контракт MessageBroker для корреляции Message
- [docs/bpmn-spec/semantics/data.md](../bpmn-spec/semantics/data.md) — интеграция ExpressionEngine (вычисление FormalExpression)
- Существующий код:
  - `pkg/thresher/thresher.go` — текущий фасад движка
  - `internal/eventproc/eventproc.go` — существующие интерфейсы распределения событий (EventHub и т.д.)
  - `internal/scope/scope.go` — существующий интерфейс Scope
  - `internal/renv/renv.go` — существующий композит RuntimeEnvironment
  - `internal/interactor/interactor.go` — существующая абстракция взаимодействия с человеком
  - `pkg/model/data/` — FormalExpression и связанные типы модели

## История документа

| Версия | Дата | Автор | Изменение |
|---|---|---|---|
| v.1 | 2026-06-09 | Руслан Габитов | **Принято.** Архитектура расширений приземлена через [SRD-004](../srd/SRD-004-extension-skeleton.md) (тот единственный фундаментальный SRD скелета): девять контрактов расширений уровня движка в `pkg/` с упакованными дефолтами, сборка через функциональные опции, разделение публичного `EngineRuntime` / внутреннего `RuntimeEnvironment` и `ExpressionEngine`/`Clock`, связанные в исполнение. Ключевые решения свёрнуты в этот Draft до приёмки (без построчных строк): `EventHub` оставлен internal (не точка расширения); взаимодействие с человеком `TaskDistributor` отложено в собственный ADR; телеметрия с дефолтами по стоимости; принцип ограниченных-in-memory-дефолтов (§4.2); кросс-адаптерная композиция через внедрённый `EngineRuntime` (§3.5 Паттерн C). Набор приёмки только-дефолты из §7 зелёный (`make ci`). Пины ADR-001 подняты v.3 → v.4. RU-twin отложен (batched). |
