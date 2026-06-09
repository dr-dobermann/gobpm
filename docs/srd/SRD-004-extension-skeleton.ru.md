# SRD-004 — Скелет расширений (минимальная/дефолтная реализация ADR-002)

| Поле | Значение |
|---|---|
| Статус | Принято |
| Версия | v.1 |
| Дата | 2026-06-09 |
| Владелец | Руслан Габитов |
| Реализует | [ADR-002 v.1 Extension Architecture](../design/ADR-002-extension-architecture.md) |
| Уточняет | [SAD-001 v.1 §11 Extension Model](../design/SAD-001-vision-and-architecture.md) |

> EN-оригинал — канонический: [SRD-004-extension-skeleton.md](SRD-004-extension-skeleton.md). Этот файл — его перевод (twin).

Этот SRD приземляет **фундаментальный скелет расширений** из [ADR-002](../design/ADR-002-extension-architecture.md): каждый контракт расширения определён в `pkg/`, каждый — со **встроенным дефолтом** (только дефолтное поведение — без production-адаптеров), сборка через функциональные опции на `thresher.New`, **разделение `EngineRuntime`/`RuntimeEnvironment`** и стартовая лог-строка — связано так, чтобы движок с нулём опций исполнял сегодняшний BPMN end-to-end. Завершение этого SRD закрывает приёмочный гейт §7 ADR-002 (строки только-дефолтов) и переводит ADR-002 → Accepted.

## 1. Предпосылки и мотивация

### 1.1 Текущее состояние (сверено с кодом)

- **У конструктора движка нет точки инъекции** — `func New(id string) (*Thresher, error)` (`pkg/thresher/thresher.go:116`); один аргумент, без опций.
- **`RuntimeEnvironment` — внутренний, с четырьмя методами** — `internal/renv/renv.go:12`: встраивает `scope.Scope`; `InstanceID() string`; `EventProducer() eventproc.EventProducer`; `RenderRegistrator() interactor.Registrator`.
- **`Instance` его уже реализует** — `internal/instance/instance.go:821` (`var _ renv.RuntimeEnvironment = (*Instance)(nil)`), с методами `InstanceID()` / `EventProducer()` / `RenderRegistrator()` (`instance.go:803/808/813`).
- **`EventHub` — внутренний** — `internal/eventproc/eventproc.go:47` (`EventHub`, `EventProducer`, `EventProcessor`); снаружи не реализуем.
- **Человеческое взаимодействие — внутреннее** — `internal/interactor` (экосистема `Registrator`, `Renderer`); достигается через `RenderRegistrator()`.
- **Выражения — модельный тип** — `pkg/model/data/expression.go:72` (`FormalExpression`); вычисляются напрямую, не за интерфейсом уровня движка.
- **Нет точек расширения инфраструктуры** — `Repository`, `Logger`, `Tracer`, `MetricsRecorder`, `Clock`, `MessageBroker`, `AuthorizationProvider`, `WorkerDispatcher` нигде не существуют (по ADR-002 §1).

### 1.2 Почему

ADR-002 (согласован с ADR-001 v.4) — это утверждённая архитектура расширений, но ничего из неё ещё не построено. По ADR-002 §5/§7 расхождения приземляются **вместе, на минимальном/дефолтном поведении, в этом одном фундаментальном SRD**; ADR-002 переходит в Accepted, когда тесты §7 этого SRD проходят. Production-адаптеры и глубина по каждому интерфейсу — это более поздние, отдельно гейтированные SRD.

## 2. Цели и область

### 2.1 Цели (в области)

- **G1.** Определить все 9 контрактов расширений в `pkg/` (по ADR-002 §4.2): `Logger` (slog-совместимый core-интерфейс — `*slog.Logger` реализует его напрямую), OTel-образные `Tracer`/`MetricsRecorder` (смоделированы по OTel API, но **core не импортирует OTel** — SAD-001 G2), интерфейсы `Clock`, `Repository`, `MessageBroker`, `ExpressionEngine`, `AuthorizationProvider`, `WorkerDispatcher`. **`EventHub` остаётся внутренним** — это исполнительная обвязка, а не точка расширения (нет use-case'а подстановки; ADR-002 §4.2). **`TaskDistributor` отложен** — кластер человеческого взаимодействия `internal/interactor` взаимосвязан и вынуждает выбор слоистости model→tasks, поэтому его промоут (и переименование `Registrator → TaskDistributor`) едет на выделенном ADR человеческого взаимодействия (ADR-001 v.4 §9), не на этом скелете.
- **G2.** Поставить в core **встроенный дефолт** для каждого: `slog.Default()` Logger · **no-op `Tracer`** (с in-memory кольцом недавних span'ов, доступным как opt-in) · **in-memory запрашиваемый, с лимитом на серии `MetricsRecorder`** (`Snapshot()` для тестов/диагностики) · `Clock` по системным часам · in-memory недолговечный `Repository` · in-memory inbox `MessageBroker` · Go-native `ExpressionEngine` (обёртка существующего вычислителя) · allow-all `AuthorizationProvider` · in-process `WorkerDispatcher`. (`EventHub` остаётся внутренним — это не дефолт публичного контракта; внутренний кластер interactor отложен.)
- **G3.** **Сборка через функциональные опции** — `thresher.New(id string, opts ...Option) (*Thresher, error)`; `defaultConfig()` связывает все дефолты; каждый `WithXxx` переопределяет один; семантика last-write; **нет `NewDefault`**. `New(id)` с нулём опций даёт рабочий движок.
- **G4.** **Стартовая лог-строка** — одна INFO-запись `thresher.starting`, перечисляющая разрешённую обвязку (по ADR-002 §4.4.1).
- **G5.** **Выделить `EngineRuntime` + расширить `RuntimeEnvironment`** (ADR-002 §4.3): определить интерфейс уровня движка/сервера `EngineRuntime` (разрешённые сервисы), реализуемый `Thresher`, **промоутнутый в `pkg/`** (публичный, путь по ADR-003); `RuntimeEnvironment` **остаётся в `internal/renv`** (он встраивает внутренние `scope.Scope`/`EventProducer`/`RenderRegistrator`) и **встраивает публичный `EngineRuntime`** + instance-local; `RenderRegistrator()` **сохранён как есть** (промоут человеческого взаимодействия отложен — ADR-001 v.4 §9); `Instance` **встраивает** `EngineRuntime` Thresher'а (методы движка промоутнуты — без per-method делегатов) и добавляет свои instance-local методы; точки вызова из track'а остаются единообразными (`t.inst.X()`).
- **G6.** **Подключить к текущему исполнению** расширения, которые сегодняшний BPMN действительно задействует: маршрутизировать вычисление `FormalExpression` через `ExpressionEngine`; брать время из `Clock` (обработка таймеров) вместо `time.Now`; использовать настроенный `Logger`. (`EventHub` — внутренний и уже подключён.) Движок исполняет текущий набор элементов без изменений.
- **G7.** **Без изменения внешнего поведения** для реализованных элементов (None Start/End, Service/User задачи, Exclusive-шлюз, sequence flow вкл. условия/default, timer-события): существующие тесты проходят без изменений и `make ci` зелёный. Ранее работавший пример (`examples/simple-timer`) по-прежнему запускается. **У `examples/timer-event` и `examples/basic-process` есть предсуществующий сбой, появившийся до SRD-004 (сломаны на master — регистрация event-start против lifecycle-гарда `Created` и дедлок на блокирующей задаче) — отслеживается [FIX-002](../fix/FIX-002-event-start-registration-lifecycle.md) и вне гейта этого SRD.**

### 2.2 Не-цели (явно отложено)

- **N1.** **Production-адаптеры** (`adapters/*` — postgres, otel, casbin, FEEL, redis/nats брокеры, …) → более поздние SRD по ADR-002 §4.6.
- **N2.** **Точки подключения в исполнении для ещё не задействованных сервисов.** `Repository`, `MessageBroker`, `AuthorizationProvider`, `WorkerDispatcher` **определены + с дефолтами + доступны** через движок и `RuntimeEnvironment`, но их точки вызова **не** подключены в этом SRD — ни одна текущая фича BPMN их пока не требует:
  - точки вызова checkpoint/load/rehydrate `Repository` → ADR Persistence & State (ADR-001 v.4 §4.7).
  - маршрутизация корреляции `MessageBroker` → SRD корреляции сообщений.
  - применение `AuthorizationProvider` на чувствительных операциях → SRD авторизации.
  - удалённый диспатч `WorkerDispatcher` → SRD распределения (по SAD-001 §13.2).
  Скелет делает их присутствующими и переопределяемыми; *вызов* их — вне области.
- **N3.** **Финальные пути пакетов `pkg/`** — ADR-003 владеет точной раскладкой. Этот SRD размещает интерфейсы по предложенной ADR-003 раскладке per-concern; пути **предварительны** до приземления ADR-003 (позднейший перенос — механическое переименование).
- **N4.** **Хук инъекции в адаптер `RuntimeAware`** (ADR-002 §3.5 Pattern C / §8.3) — скелет **определяет** `EngineRuntime` и `RuntimeEnvironment`, но адаптеров для инъекции нет, поэтому хук `UseRuntime(EngineRuntime)` и его обвязка на этапе сборки приземляются с первым реальным адаптером.
- **N5.** **Хелперы контракт-тестов адаптеров, опциональные интерфейсы побочных способностей (`Starter`/`Stopper`/…), осведомлённость о кластере** (ADR-002 §8.3/§8.4) — приземляются с первым реальным адаптером.
- **N6.** **Промоут взаимодействия человек-задача.** Кластер `internal/interactor` (`Registrator`/`Interactor`/`RenderController`) остаётся внутренним; переименование `Registrator → TaskDistributor` и любое раскрытие на уровне движка едут на выделенном **ADR человеческого взаимодействия** (ADR-001 v.4 §9). Скелет поставляет **10** контрактов, а существующий `RenderRegistrator()` уровня instance не тронут.

## 3. Требования

### 3.1 Функциональные

| # | Требование | Приёмка |
|---|---|---|
| FR-1 | 9 контрактов существуют в `pkg/` (G1). `EventHub` остаётся внутренним (ADR-002 §4.2); `TaskDistributor` отложен (ADR человеческого взаимодействия). | `grep` находит каждый интерфейс под `pkg/`; сборка проходит; реализации в `internal/` импортируют интерфейсы из `pkg/`. |
| FR-2 | У каждого контракта есть встроенный core-дефолт (G2). | Дефолтное значение существует и удовлетворяет своему интерфейсу; конструируется через `defaultConfig()`. |
| FR-3 | `thresher.New(id, opts ...Option)`; ноль опций работает; `WithXxx` переопределяет; last-write; нет `NewDefault`. | `New("x")` запускается; `WithLogger(a),WithLogger(b)` ⇒ b; нет символа `NewDefault`. |
| FR-4 | Одна INFO-лог-строка `thresher.starting` перечисляет каждое разрешённое расширение движка по типу реализации. | Тест с capture-логгером: ровно одна запись, ключ `thresher.starting`, атрибуты по ADR-002 §4.4.1. |
| FR-5 | `EngineRuntime` (сервисы движка) определён в **публичном** `pkg/renv`, реализован `Thresher`; `RuntimeEnvironment` **остаётся внутренним**, встраивает его + instance-local (вкл. сохранённый `RenderRegistrator()`); `Instance` **встраивает** `EngineRuntime` Thresher'а. | `var _ renv.EngineRuntime = (*Thresher)(nil)` (публичный) и `var _ internalrenv.RuntimeEnvironment = (*Instance)(nil)`. |
| FR-6 | `Instance` встраивает `EngineRuntime` Thresher'а (методы движка промоутнуты, без per-method делегатов); track достигает сервисов через свой единственный `*Instance`. | Тест по каждому методу: `instance.Logger()` (и т.д.) == настроенное значение движка. |
| FR-7 | `ExpressionEngine`, `Clock`, `Logger` подключены в текущее исполнение (G6): `FormalExpression` вычисляется через `ExpressionEngine`; время таймера через `Clock`. (`EventHub` — внутренний и уже подключён.) | Тесты override-and-observe: кастомный `ExpressionEngine`/`Clock` — именно тот, что используется во время исполнения. |
| FR-8 | `Repository`/`MessageBroker`/`AuthorizationProvider`/`WorkerDispatcher` определены, с дефолтами и достижимы через движок/RE, но **не** вызываются исполнением (N2). | Они конструируемы и доступны (`instance.Repository()` и т.д.); ни одна точка вызова в исполнении их пока не ссылается. |
| FR-9 | Без регрессий для реализованных элементов (G7). | Существующие тесты `internal/instance` + движка и `examples/*` проходят без изменений; `make ci` зелёный. |

### 3.2 Нефункциональные

| # | Требование | Приёмка |
|---|---|---|
| NFR-1 | Без гонок под детектором. | `make ci` (с гейтом гонок) зелёный. |
| NFR-2 | Тронутые/созданные файлы соответствуют стандарту покрытия (≥80%, цель 100%; гейт `covercheck`). | `make cover-check` PASS на diff'е. |
| NFR-3 | `core` не получает ни одной runtime-зависимости вне stdlib (SAD-001 G2). Соблюдается даже для телеметрии: `Tracer`/`MetricsRecorder` OTel-*образны*, но core **не** импортирует OTel (ADR-002 §4.2); реальные типы OTel живут в `adapters/otel/`. | `go mod graph` не показывает новых внешних core-зависимостей (дефолты только stdlib; `slog` — stdlib; нет `go.opentelemetry.io/*` в core). |
| NFR-4 | Видимая по умолчанию observability сохранена (дефолт Logger — `slog.Default()`). | Движок с нулём опций логирует в дефолтный handler. |

## 4. Дизайн и план реализации

### 4.1 Формы (иллюстративно; точные пути `pkg/` — по ADR-003)

```go
// Конфиг уровня движка держит разрешённые расширения (по одному на интерфейс).
type thresherConfig struct {
    logger      Logger          // slog-satisfiable interface; default slog.Default()
    tracer      Tracer          // OTel-shaped, core-defined (no OTel import); default no-op
    metrics     MetricsRecorder // default = in-memory queryable, series-capped registry
    clock       Clock
    repository  Repository
    msgBroker   MessageBroker
    exprEngine  expression.Engine
    authz       AuthorizationProvider
    dispatcher  WorkerDispatcher
    // EventHub is NOT here — it stays internal (ADR-002 §4.2); the Thresher
    // constructs its internal hub itself, not via an option.
}

type Option func(*thresherConfig)

func WithLogger(l Logger) Option { return func(c *thresherConfig) { c.logger = l } }  // *slog.Logger satisfies Logger
// … one per extension …

func New(id string, opts ...Option) (*Thresher, error) {
    cfg := defaultConfig()          // all defaults wired
    for _, o := range opts { o(&cfg) }
    t, err := assemble(id, cfg)
    if err != nil { return nil, err }
    t.logStartupConfig()            // §4.4.1
    return t, nil
}
```

По ADR-002 §4.3 сервисы движка выделены в интерфейс **`EngineRuntime`**, который реализует `Thresher` (возвращая разрешённые значения `thresherConfig`); `RuntimeEnvironment` (остаётся внутренним; в `pkg/` промоутнут только `EngineRuntime`) **встраивает `EngineRuntime`** + instance-local методы; `Instance` **встраивает** `EngineRuntime` Thresher'а (методы движка промоутнуты — без per-method делегатов) и сохраняет свои instance-local методы. Точки вызова из track'а не меняются по стилю (`t.inst.Clock().Now()`).

### 4.2 Вехи (каждая независимо собираема + CI-зелёная)

1. **M1 — Observability + Clock.** Интерфейсы `Logger` (slog-совместимый), OTel-образные `Tracer`/`MetricsRecorder` (без импорта OTel), `Clock` + дефолты: `slog.Default()` Logger, **no-op Tracer**, **in-memory запрашиваемый реестр Metrics с лимитом серий**, `Clock` по системным часам — плюс opt-in in-memory кольцо недавних span'ов Tracer для dev/тестов. Чистые leaf-пакеты; обвязки движка пока нет. Тесты дефолтов/конформанса (вкл. `Snapshot()` реестра + поведение лимита серий).
2. **M2 — Stateful-листья.** Интерфейсы `Repository`, `MessageBroker`, `AuthorizationProvider`, `WorkerDispatcher` + дефолты (in-mem / in-mem inbox / allow-all / in-proc). Определены + протестированы; пока не вызываются (N2).
3. **M3 — ExpressionEngine.** `ExpressionEngine` (интерфейс `pkg/model/expression`, оборачивающий вычислитель `FormalExpression`) + Go-native дефолт (`pkg/model/expression/goexpr`). Только определение + дефолт; маршрутизация точек вызова — M5/G6. `EventHub` **остаётся внутренним** (ADR-002 §4.2), а `TaskDistributor` **отложен** (ADR-001 v.4 §9) — ни один не промоутится, поэтому M3 — чистое добавление нового пакета без churn'а импортёров.
4. **M4 — Сборка.** `thresherConfig` + `Option` + `WithXxx` (по одному на расширение) + рефакторинг `New(id, opts...)` + `defaultConfig()` + `logStartupConfig()` (§4.4.1). Нет `NewDefault`.
5. **M5 — EngineRuntime + RuntimeEnvironment.** Промоут `EngineRuntime` (сервисы движка) в **публичный `pkg/renv`**, реализуемый `Thresher`; `RuntimeEnvironment` **остаётся в `internal/renv`**, встраивает публичный `EngineRuntime` и сохраняет `RenderRegistrator()` как есть; `Instance` встраивает `EngineRuntime` Thresher'а; перенаправить импорты; **подключить ExpressionEngine + Clock в исполнение** (G6).
6. **M6 — Приёмка.** Применимый набор ADR-002 §7 (zero-option New e2e, опции compose/override/last-write, стартовый лог, Instance-implements-RE, делегаты, конформанс дефолтов, композиция RE) + examples проходят + `make ci` зелёный + `cover-check` PASS. Flip **ADR-002 + SRD-004 → Accepted** + RU-twin'ы.

Последовательность: листья (M1/M2) и промоуты (M3) определяют контракты+дефолты без связи с движком; сборка (M4) подключает их в `New`; расширение RE (M5) раскрывает их через `Instance` и подключает исполняемые; M6 верифицирует + принимает.

## 5. Верификация (Definition of Done)

Отображается на [ADR-002 §7](../design/ADR-002-extension-architecture.md) (строки только-дефолтов; зависящие от адаптеров строки отложены на первый SRD адаптера):

| Тест | Утверждает |
|---|---|
| Zero-option New e2e | `thresher.New("t")` регистрирует + исполняет процесс до завершения со всеми дефолтами. |
| Опции compose / override / last-write | все `WithXxx` в случайном порядке ⇒ одно состояние; каждая переопределяет свой дефолт; побеждает последняя запись. |
| Лог стартового конфига | ровно одна INFO `thresher.starting` с атрибутом на каждое расширение движка = подключённый тип реализации. |
| Thresher реализует EngineRuntime; Instance реализует RuntimeEnvironment | `var _ renv.EngineRuntime = (*Thresher)(nil)` и `var _ renv.RuntimeEnvironment = (*Instance)(nil)`. |
| Делегаты сервисов движка | `instance.X()` == X из конфига движка, по каждому методу. |
| Конформанс дефолтов | in-memory дефолт `Repository` (и прочие) проходят тест дефолтного поведения. |
| Обвязка исполняемых расширений | кастомный `ExpressionEngine`/`Clock` — именно тот, что используется во время исполнения (FR-7). |
| Без регрессий | существующий набор проходит без изменений; `examples/simple-timer` запускается; `make ci` зелёный; `cover-check` PASS. (`timer-event`/`basic-process` предсломаны — FIX-002, вне области.) |

**DoD:** все FR/NFR выполнены; таблица выше зелёная; `make ci` + `cover-check` зелёные; ADR-002 §7 (применимые строки) выполнены. **Достигнуто 2026-06-09** — ADR-002 + SRD-004 переведены в Accepted. RU-twin'ы **отложены** (батчатся до того, как осядет работа по FIX-002 / примерам, по политике bilingual-docs).

## 6. Риски и регрессии

- **Перенос RuntimeEnvironment (`internal/renv`→`pkg/renv`) задевает импорты.** Митигация: M5 — отдельная веха; перенаправить всех импортёров; гейты `make ci`.
- **Косвенность ExpressionEngine меняет путь вычисления.** Митигация: дефолт оборачивает *существующий* вычислитель; тест переопределения FR-7 + набор без регрессий (G7).
- **Расползание области в точки подключения N2.** Repository/MessageBroker/AuthZ/WorkerDispatcher — только определение-и-дефолт; не подключать точки вызова здесь.
- **Предварительные пути пакетов (ADR-003).** Митигация: пути по предложенной ADR-003 раскладке; позднейший перенос механический.
- **Расползание зависимостей `core`.** Митигация: все дефолты только stdlib; проверка `go mod graph` NFR-3.

## 7. Итог реализации

Приземлено на ветке `feat/extension-skeleton`, по одному commit'у на веху:

- **M1** `91a2fdf` — `pkg/observability` (`Logger`/`Tracer`/`MetricsRecorder` + `noop`/`memmetrics`/`memtrace`) + `pkg/clock` (`Clock` + `syscl`/`clocktest`).
- **M2** `8d7e291` — `pkg/repository` (+`memrepo`), `pkg/messaging` (`MessageBroker` +`membroker`), `pkg/auth` (+`allowall`), `pkg/tasks` (`WorkerDispatcher` +`localdispatcher`); ограниченные дефолты, определены-но-не-вызываются (N2).
- **M3** `ef77485` — `pkg/model/expression.Engine` (+`goexpr` дефолт).
- **M4** `beaf13c` — функциональные опции `pkg/thresher` (девять `WithXxx`) + `defaultConfig` + `New(id, opts...)` + стартовый лог; отвержение nil-опций усилено в `e5cb184`.
- **M5** `ffc534b` — `pkg/renv.EngineRuntime` (публичный); `RuntimeEnvironment` встраивает его (внутренний); `thresherConfig` и `Instance` реализуют/встраивают его; EventHub + ожидатели таймеров инъектируются им; `ExpressionEngine` + `Clock` подключены в точки вызова exclusive-шлюза и таймера. `internal/enginert` предоставляет дефолтный рантайм для тестов.

**Отклонения от исходного плана (все согласованы по ходу):** `EventHub` остаётся внутренним (не точка расширения); `TaskDistributor` человеческого взаимодействия отложен на свой ADR (N6); `EngineRuntime` — разделяемое значение, инъектируемое в instance'ы и EventHub (Решение B, без цикла импортов); дефолты телеметрии распределены по стоимости (in-mem метрики включены по умолчанию, no-op трейсер); принцип ограниченных in-memory дефолтов (ADR-002 §4.2).

**V-результаты (2026-06-09):** `make ci` зелёный — tidy / lint (0 проблем × 6 модулей) / build-all (вкл. examples) / race `test-all` / `cover-check` / govulncheck. Diff-покрытие 99.5% изменённых строк (3 непокрытых — недостижимая ошибочная ветка `eventhub.New`). Набор только-дефолтов ADR-002 §7 зелёный. `examples/simple-timer` исполняется end-to-end. `examples/timer-event` и `examples/basic-process` предсломаны (FIX-002) — исключены из G7.

## 8. Ссылки

- [ADR-002 v.1 Extension Architecture](../design/ADR-002-extension-architecture.md) — §3.5 межадаптерная композиция (EngineRuntime / Pattern C), §4.2 каталог, §4.3 EngineRuntime + RuntimeEnvironment, §4.4 сборка, §4.5 дефолты, §5 расхождения, §7 приёмочный гейт (этот SRD закрывает строки только-дефолтов), §8.3 `RuntimeAware`.
- [ADR-001 v.4 Execution Model](../design/ADR-001-execution-model.md) — §4.7 инварианты рантайма (цель Repository); §4.3 поток событий (потребители EventHub/Logger).
- [ADR-003 v.1 Module Layout](../design/ADR-003-module-layout.md) — финальные пути `pkg/` (здесь предварительны).
- [SAD-001 v.1 §11 Extension Model](../design/SAD-001-vision-and-architecture.md); §9.2 multi-module; G2 minimal-core-deps.
- Существующий код: `pkg/thresher/thresher.go:116`; `internal/renv/renv.go:12`; `internal/eventproc/eventproc.go:47`; `internal/interactor/`; `pkg/model/data/expression.go:72`; `internal/instance/instance.go:803-821`.

## 9. Открытые вопросы

1. **Группировка пакета observability** — один `pkg/observability` (Logger/Tracer/MetricsRecorder) против отдельных `pkg/logger`,`pkg/tracer`,`pkg/metrics`? ADR-003 §3.2 предпочитает «один подпакет на цельный concern». **Разрешено предварительно:** группировать как `pkg/observability` для цельных стоков; финальное решение едет на ADR-003. Подтвердить на M1.
2. **Размещение `MetricsRecorder`?** **Разрешено:** `MetricsRecorder()` — метод на `EngineRuntime` (ADR-002 §4.3); поскольку `RuntimeEnvironment` встраивает `EngineRuntime`, он достижим и со стороны движка (`Thresher`), и со стороны instance (`Instance`) без особых случаев.
3. **Форма интерфейса `ExpressionEngine`** — минимальный `Evaluate(expr data.FormalExpression, scope scope.Scope) (any, error)`? **Разрешено предварительно:** зеркалить сигнатуру вызова текущего вычислителя, чтобы дефолт был тонкой обёрткой; зафиксировать точную сигнатуру на M3 из точек вызова.
4. **Дефолтная реализация телеметрии** — no-op против видимой против log-backed? **Разрешено (дизайн-обсуждение):** дефолты различаются по стоимости сигнала (ADR-002 §4.2). **Метрики** по умолчанию — in-memory, с лимитом серий, запрашиваемый реестр (видимый по умолчанию по политике observability; `Snapshot()` делает тесты тривиальными — logtel не нужен). **Трейсинг** по умолчанию no-op (span — это аллокация на событие, инертная без бэкенда) с in-memory кольцом недавних span'ов как однострочный opt-in. Персистентный SQL-сток телеметрии — будущий production-адаптер (`adapters/sqlstore`), никогда не core-дефолт. Прежняя идея log-backed телеметрии **отброшена** (она превращает метрики в лог-текст, который надо парсить обратно).

## История документа

| Версия | Дата | Автор | Изменение |
|---|---|---|---|
| v.1 (Accepted) | 2026-06-09 | Руслан Габитов | **Принято.** Все вехи (M1–M5) приземлены; `make ci` зелёный; набор только-дефолтов ADR-002 §7 пройден; diff-покрытие 99.5%. G7 выполнено для существующих тестов + `simple-timer`; `timer-event`/`basic-process` предсломаны, выведены в FIX-002. RU-twin отложен (батчится). Пред-приёмочная Draft-итерация свёрнута без per-round строк. Пины ADR-001 подняты v.3 → v.4. |
| v.1 | 2026-06-08 | Руслан Габитов | Первый Draft. Фундаментальный скелет расширений для ADR-002 — 9 контрактов в `pkg/` + встроенные дефолты, сборка через функциональные опции, разделение `EngineRuntime` (публичный) / `RuntimeEnvironment` (внутренний) (Thresher реализует `EngineRuntime`; `RuntimeEnvironment` встраивает его; `Instance` встраивает его), стартовый лог; исполняемые расширения (ExpressionEngine/Clock/Logger) подключены, остальные только определение-и-дефолт (N2); `EventHub` остаётся внутренним (не точка расширения); инъекция в адаптер `RuntimeAware` отложена (N4); человеческое взаимодействие/`TaskDistributor` отложено на свой ADR (N6). Закрывает строки только-дефолтов ADR-002 §7 при приземлении. |
