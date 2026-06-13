# SRD-009 — Вычисление single-set I/O

| Поле | Значение |
|---|---|
| Статус | Принято |
| Версия | v.1 |
| Дата | 2026-06-13 |
| Владелец | Руслан Габитов |
| Реализует | [ADR-011 v.2 Process Data Flow](../design/ADR-011-process-data-flow.md) |
| Уточняет | [ADR-010 v.1 Process Data Model](../design/ADR-010-process-data-model.md) |

> EN-оригинал — канонический: [SRD-009-single-set-io-evaluation.md](SRD-009-single-set-io-evaluation.md). Этот файл — его перевод (twin).

Этот SRD приземляет [ADR-011 v.2](../design/ADR-011-process-data-flow.md) §2.2–§2.5 + решение §2.7 «нет типа `Set`»: он **отбрасывает реифицированный тип `Set`**, делает required/optional/while-executing **атрибутами на каждом параметре** и добавляет **рантайм-гейты доступности** — required-входы должны быть доступны на старте (иначе fail-fast-ошибка, никогда не ожидание), а required-выходы должны быть произведены на завершении (иначе ошибка). Он строится на машинерии data-flow, уже приземлённой SRD-007 (execution frame, data associations, протокол load→commit); он **не** переписывает выполнение associations.

## 1. Контекст и мотивация

### 1.1 Текущее состояние (сверено с кодом)

- **Машинерия выполнения уже существует** (SRD-007 / ADR-010). `Association.calculate` (`pkg/model/data/association.go:246`) делает transformation/single-source-копирование; `task.LoadData` (`pkg/model/activities/task.go:84`) запускает **input**-associations и заполняет frame; `task.UploadData` (`task.go:190`) запускает **output**-associations и коммитит; протокол frame load→commit/discard жив (`internal/scope/frame.go`, шаги `NodeDataConsumer`/`NodeDataProducer` `track.go:641-667`); events связывают данные так же (`pkg/model/events/event.go:245-389`). Рантайм читает I/O **только** через `IoSpec.Parameters(dir)` (`io_spec.go:109`).
- **`Set` — реифицированный тип, который при одном наборе на activity не несёт информации.** `Set` (`io_spec_obj.go:99`) имеет 12 методов (`io_spec_obj.go:146-397`) и держит `values map[SetType][]*Parameter`. `InputOutputSpecification` уже держит `params map[Direction][]*Parameter` **и** `sets map[Direction][]*Set` (`io_spec.go:86`) — поднаборная принадлежность параметров дублирует `params`. Рантайм никогда не ссылается на `data.Set`; только конструирование (`activities/activity_options.go`, `events/*_options.go`, `events/event.go`/`end.go`, `examples/process-data`).
- **Бакеты `SetType` подменяют собой атрибуты optional/while-executing на параметре.** `SetType` (`io_spec.go:15`) — bit-flag-enum (`DefaultSet`/`OptionalSet`/`WhileExecutionSet`, `io_spec.go:18-26`); «роль» параметра — его членство по `SetType` внутри `Set`. На `Parameter` **нет атрибута `optional` / `whileExecuting`** (`Parameter` это `{name, ItemAwareElement}`, `io_spec_obj.go`).
- **`Set.Link`/`Unlink`/`linkedSets` — мёртвый код** — определён в `io_spec_obj.go:298-340`, не используется ничем вне собственных тестов (единственная «multi-set/IORule-подобная» машинерия).
- **Нет гейта доступности на старте; нет проверки required-выхода на завершении.** `task.LoadData` выполняет input-associations и ошибается *неявно*, если source не `Ready` (`association.go:258`); он не проверяет «required-входы activity доступны» как полноправный гейт и не знает понятия *optional*-входа, законно отсутствующего. `task.UploadData` проталкивает output-associations, но не проверяет, что **required**-выходы реально произведены. Проверка времени конструирования `Set.Validate` «default-параметры Ready?» (`io_spec_obj.go:356`) — это стенд-ин §10.4.2, который называет ADR-011 §1.2 — она исполняется на сборке, а не как рантайм-гейт.

### 1.2 Зачем

[ADR-011 v.2](../design/ADR-011-process-data-flow.md) решает: ровно один input/output-набор на activity, с required/optional/while-executing как **атрибутами на каждом параметре** и **без типа `Set`** (§2.2, §2.7); availability гейтит старт, но никогда не ждёт (§2.3); непроизведённый required-выход — ошибка (§2.2). Модельная машинерия на месте; не хватает (a) упрощения модели — отбросить `Set`, пометить параметры — и (b) рантайм-гейтов, превращающих «запусти associations и надейся» в «требуй required, разреши optional, падай быстро на отсутствующем». Этот SRD приземляет оба.

## 2. Цели и охват

### 2.1 Цели (в охвате)

- **G1.** `Parameter` несёт булевы `optional` и `whileExecuting` (по умолчанию `false` = required, не while-executing). Required/optional/while-executing — атрибут на параметре, а не членство в наборе.
- **G2.** Реифицированный тип `Set` удаляется: `Set`, `SetType`, `allTypes`, `AllSets`, `Set.Link`/`Unlink`/`linkedSets`. `InputOutputSpecification` владеет своими `Parameter`'ами напрямую (убирает поле `sets` и `AddSet`/`RemoveSet`/`Sets`) и выставляет `InputSet()`/`OutputSet()` как views над своими списками входных/выходных параметров.
- **G3.** Конструирование activity и event объявляет входы/выходы как **помеченные параметры**, а не наборы — машинерия `WithSet`/`WithEmptySet`/`setDef` и поля `inputSet`/`outputSet` `*data.Set` заменяются.
- **G4.** **Start-gate (входы).** Когда activity/throw-event готова, каждый **required**-вход должен быть доступен; недоступный required-вход — **fail-fast-ошибка/инцидент — никогда не ожидание** (§2.3). **Optional**-вход может отсутствовать (его association пропускается, вход остаётся `Unavailable`).
- **G5.** **Completion-gate (выходы).** На завершении каждый **required**-выход должен быть произведён (`Ready`); отсутствующий required-выход — ошибка (§2.2 «gobpm никогда молча не производит ничего»). Optional-выход может отсутствовать.
- **G6.** Без изменения поведения для существующих корректных примеров/тестов сверх новых гейтов: все пять примеров запускаются; путь frame/association/commit из SRD-007 в остальном не тронут.

### 2.2 Не-цели (отложено, у каждой назван дом)

- **Рантайм-вычисление `whileExecuting`** — флаг `whileExecuting` *хранится* (G1), но его вычисление в середине выполнения (вход, подтягиваемый / выход, проталкиваемый *во время* выполнения, а не на старте/завершении) **отложено** до работы по типам task, которой оно нужно; гейты этого SRD рассматривают только не-while-executing-параметры.
- **Множественные I/O sets, упорядоченный выбор, IORule-спаривание** — не-цель по ADR-011 v.2 §2.8 (повторное добавление означало бы повторное введение абстракции `Set`).
- **Service data reader** (ADR-011 §2.6) и **examples-final-pass-демо** — следующий SRD.
- **Особый случай данных уровня Start/End процесса** (process `DataInput`/`DataOutput`) — приземляется с работой messaging/call-activity (ADR-011 §2.5).
- Разделение value/notification §2.7 и унификация event-options — свои SRD (как в SRD-008 §2.2).

## 3. Требования

### 3.1 Функциональные

| # | Требование |
|---|---|
| FR-1 | `Parameter` (`io_spec_obj.go`) получает `optional bool` и `whileExecuting bool`. `NewParameter` принимает опции `data.Optional()` / `data.WhileExecuting()` (по умолчанию обе `false`); аксессоры `Parameter.IsOptional()` / `Parameter.IsWhileExecuting()`. |
| FR-2 | Удалить тип `Set` и его методы (`io_spec_obj.go:99-397`), `SetType`/`allTypes`/`AllSets`/`SingleType`/`CombinedTypes` (`io_spec.go:15-71`), включая мёртвые `Link`/`Unlink`/`linkedSets`. |
| FR-3 | `InputOutputSpecification` (`io_spec.go:86`) убирает поле `sets map[Direction][]*Set` и методы `AddSet`/`RemoveSet`/`Sets`; сохраняет `params map[Direction][]*Parameter`, `Parameters(dir)`, `AddParameter`, `RemoveParameter` (последний теряет поднаборное удаление — наборов нет). Добавляет `InputSet() []*Parameter` и `OutputSet() []*Parameter` как views над `params[Input]`/`params[Output]`. |
| FR-4 | `InputOutputSpecification.Validate` переписывается как **структурная** проверка (нет дублей имён параметров по направлению; нет nil-параметра) — прежние проверки «параметр принадлежит ≥1 набору» / «default-параметры Ready» удалены (наборов нет; готовность — рантайм-забота, FR-6). |
| FR-5 | Конструирование activity (`activities/activity_options.go`) заменяет `WithSet`/`WithEmptySet`/`setDef`/`addSetParams` опциями со списком параметров — `WithParameters(dir data.Direction, params ...*data.Parameter)` (каждый параметр несёт свои `optional`/`whileExecuting`); `WithoutParams()` остаётся (пустой I/O). `createIOSpecs` строит `IoSpec` из помеченных списков параметров. Events (`events/end_options.go`, `start_options.go`, `event.go`, `end.go`) убирают поля `inputSet`/`outputSet` `*data.Set` и сохраняют свои списки `dataInputs`/`dataOutputs`. |
| FR-6 | **Start-gate.** Перед тем как activity/throw-event выполнит свои input-associations, гейт проверяет, что каждый **required** (`!optional && !whileExecuting`) вход доступен — т.е. его заполняющая association может выполниться (source `Ready`). Недоступный required-вход → классифицированная **ошибка** (`errs`-класс, никогда не ожидание, §2.3). Optional-вход, чья association не может выполниться, пропускается и остаётся `Unavailable`. Вшито в `task.LoadData` (`task.go:84`) и `throwEvent.LoadData` (`event.go:363`). |
| FR-7 | **Completion-gate.** После выполнения, перед/на коммите, гейт проверяет, что каждый **required**-выход произведён (`Ready`); отсутствующий required-выход → классифицированная **ошибка**. Вшито в `task.UploadData` (`task.go:190`). Optional-выходы могут отсутствовать. |
| FR-8 | Оба гейта поднимают **само-идентифицирующиеся** ошибки (id activity/event + имя параметра + направление), по правилу валидации публичного API. |

### 3.2 Нефункциональные

| # | Требование |
|---|---|
| NFR-1 | Без изменения поведения для корректных процессов сверх новых гейтов: тесты data / activities / events / process / instance / thresher проходят; все пять примеров запускаются до exit 0. |
| NFR-2 | Путь SRD-007 в остальном не тронут — `IoSpec.Parameters(dir)`, `Association.calculate`, `Frame.Commit/Discard` сохраняют сигнатуры и семантику. |
| NFR-3 | `make ci` зелёный на каждом milestone; diff-coverage ≥95 % (цель 100 %) на затронутых файлах. |
| NFR-4 | Каждый новый/изменённый публичный символ несёт doc-comment; новый публичный API (опции параметра, `WithParameters`, гейты) валидирует свои входы само-идентифицирующимися ошибками. |

## 4. Дизайн и план реализации

### 4.1 Модель: параметры несут свою роль; IoSpec ими владеет

```mermaid
flowchart LR
    subgraph after["after (no Set type)"]
        IOS["InputOutputSpecification\nparams[Input], params[Output]"]
        P1["Parameter\nname, ItemAwareElement,\noptional, whileExecuting"]
        IOS -->|InputSet() / OutputSet() views| P1
    end
```

`Set` исчезает. `IoSpec.params[Input]` **есть** input-набор; `params[Output]`
**есть** output-набор; `InputSet()`/`OutputSet()` — read-only views (словарь
BPMN). `Parameter` несёт `optional` (по умолчанию `false` → required) и
`whileExecuting` (по умолчанию `false`). `IoSpec.Validate` становится структурной
проверкой.

### 4.2 API конструирования

- **Опции параметра.** `data.Optional()` и `data.WhileExecuting()` ставят флаги во
  время `NewParameter`. Required-вход — по умолчанию: помечаешь исключения,
  совпадая со стандартом (`optional` по умолчанию `false`).
- **Activities.** `WithParameters(dir, params...)` добавляет параметры направления
  (каждый предварительно помечен); `WithoutParams()` держит случай пустого I/O.
  `WithSet` / `WithEmptySet` / `setDef` / `addSetParams` удалены.
- **Events.** `endEvent`/`startEvent` сохраняют списки `dataInputs`/`dataOutputs`
  и убирают параллельные `inputSet`/`outputSet` `*data.Set`.

### 4.3 Рантайм-гейты

- **Start-gate** (`task.LoadData`, `throwEvent.LoadData`): разбить входы на required
  (`!optional && !whileExecuting`) и остальные. Для каждого required-входа его
  заполняющая association должна выполниться (source `Ready`); если не может — гейт
  возвращает классифицированную ошибку и frame отбрасывается — **никакого
  ожидания** (§2.3). Optional-входы, чья association не может выполниться,
  пропускаются (остаются `Unavailable`). Это превращает сегодня-неявный отказ в
  явный, optional-осведомлённый гейт.
- **Completion-gate** (`task.UploadData`): после выполнения output-associations
  каждый required-выход (`!optional && !whileExecuting`) должен быть `Ready`; иначе
  классифицированная ошибка (frame не коммитится).
- Оба гейта переиспользуют существующее выполнение associations и аксессоры
  состояния данных (`ItemAwareElement.State()`), добавляя лишь разбиение +
  проверки required-availability/required-production.

### 4.4 Milestones (каждый = один коммит, CI-зелёный)

- **M1 — отбросить `Set`, пометить параметры** (FR-1/2/3/4/5). Атомарно по
  `pkg/model/data`, `pkg/model/activities`, `pkg/model/events`,
  `examples/process-data` и их тестам (удаление типа не может быть частичным и
  остаться CI-зелёным). Сохраняет поведение: рантайм всё ещё читает
  `IoSpec.Parameters(dir)`; гейта пока нет. *(При желании можно разделить
  expand→contract — добавить flagged-param-API рядом с `Set`, мигрировать, затем
  удалить `Set` — поднимается на milestone-plan-гейте.)*
- **M2 — start-gate** (FR-6/8). Проверка доступности required-входа в
  `task.LoadData` + `throwEvent.LoadData`; optional-осведомлённая; no-wait-ошибка.
- **M3 — completion-gate** (FR-7/8). Проверка производства required-выхода в
  `task.UploadData`.

### 4.5 Тесты (по milestone; детали в §5)

`io_spec_test.go` / `values_test.go` (нет `Set`; views `InputSet()`/`OutputSet()`;
`Validate` структурная; флаги параметра + аксессоры), тесты `activity_options` /
`service_task` (`WithParameters`, `WithoutParams`), тесты events (списки
параметров, нет `Set`), тесты `task` (start-gate: required-отсутствует → ошибка,
optional-отсутствует → ok; completion-gate: required-выход-отсутствует → ошибка) и
пять примеров как smoke.

## 5. Проверка (Definition of Done)

| # | Проверка | Ожидание |
|---|---|---|
| V1 | `data.Set`/`SetType`/`AllSets`/`Link`/`linkedSets` больше не существуют; `grep` не находит ссылок по репозиторию; пакеты собираются (FR-2). | gone |
| V2 | `Parameter` несёт `optional`/`whileExecuting` с опциями + аксессорами, по умолчанию required (FR-1). | зелено |
| V3 | У `IoSpec` нет `sets`/`AddSet`/`RemoveSet`/`Sets`; `InputSet()`/`OutputSet()` возвращают списки параметров; `Validate` структурная (отвергает дубли имён, принимает корректную спеку) (FR-3/4). | зелено |
| V4 | Activities строят I/O через `WithParameters`/`WithoutParams`; events через списки параметров; поля `*data.Set` не осталось (FR-5). | зелено |
| V5 | Start-gate: required-вход, чей source недоступен → классифицированная ошибка, frame отброшен, **никакого ожидания**; optional-вход отсутствует → activity продолжает (FR-6/8). | зелено |
| V6 | Completion-gate: required-выход не произведён → классифицированная ошибка, нет коммита; optional-выход отсутствует → коммит продолжается (FR-7/8). | зелено |
| V7 | Регрессия: data / activities / events / process / instance / thresher проходят; все пять примеров запускаются до exit 0 (NFR-1). | зелено |
| V8 | `make ci` зелёный; diff-coverage ≥95 % на затронутых файлах (NFR-3). | pass |

## 6. Риски и регрессии

- **Большой атомарный M1.** Удаление `Set` затрагивает data + activities + events
  + примеры + тесты в одном коммите. Митигировано тем, что рантайм читает только
  `IoSpec.Parameters(dir)` (не изменён), так что изменение структурное; V7 (все
  примеры + наборы тестов) — бэкстоп. Доступно разделение expand→contract, если
  атомарный diff слишком велик для ревью.
- **Start-gate слишком строгий.** Превращение неявного отказа association в явный
  гейт могло бы отвергнуть процесс, «работавший» случайно. Сужено до *required*-
  входов (optional учтён); V5 покрывает оба направления, а V7 доказывает, что
  примеры всё ещё запускаются.
- **Completion-gate ломает task'и без выходов.** Сегодняшний типовой task не
  производит выходов; пустой output-набор не имеет *required*-выходов, так что гейт
  для него no-op. V6/V7 подтверждают.
- **`whileExecuting` хранится, но инертен.** Флаг существует, но ничто его пока не
  вычисляет; гейты явно исключают while-executing-параметры, так что он не может
  дать ложный результат гейта. Названная отсрочка (§2.2).

## 7. Сводка реализации

Приземлено на ветке `feat/io-set-evaluation` (после правки ADR-011 v.2 и
doc-коммита) тремя milestone-коммитами; `make ci` зелёный и diff-coverage ≥95% на
затронутых файлах на каждом.

### 7.1 Milestones по коммитам

| Milestone | Коммит | Охват | Тесты |
|---|---|---|---|
| ADR-011 v.2 | `89ca4fd` | правка концепции drop-Set | — |
| Doc | `57152f9` | SRD-009 | — |
| M1 — отбросить Set, пометить параметры | `75a8105` | удалить `Set`/`SetType`; флаги + опции `Parameter`; `IoSpec` владеет параметрами + `InputSet()`/`OutputSet()`; `WithParameters`; events убирают `*data.Set` | `TestParameter`, `TestIOSpec`, `TestIOSpecValidateDuplicateName`, `TestRequiredItemIDs` (+ мигрированные тесты activity/event) |
| M2 — start-gate | `8530583` | `data.RequiredItemIDs`; гейт доступности required-входа в `task.LoadData` / `throwEvent.LoadData` (нет ожидания; optional пропускается) | `TestTaskStartGate`, `TestThrowEventStartGate` |
| M3 — completion-gate | `0296ab4` | гейт производства required-выхода в `updateOutputs` / `UploadData` (отсутствие optional разрешено) | `TestTaskCompletionGate` |

### 7.2 Результаты проверки (§5)

- **V1–V4** — `Set`/`SetType` устранены (нет ссылок в репозитории); флаги +
  аксессоры `Parameter`; views `IoSpec.InputSet()`/`OutputSet()`; структурный
  `Validate`; конструирование через `WithParameters`/`WithoutParams`; нет поля
  `*data.Set`. Зелено.
- **V5** — start-gate: required-вход недоступен → классифицированная ошибка, нет
  ожидания; optional-вход отсутствует → продолжает. Зелено.
- **V6** — completion-gate: required-выход не произведён → ошибка; optional-выход
  отсутствует → коммит продолжается. Зелено.
- **V7** — наборы тестов data / activities / events / process / instance /
  thresher проходят; все пять примеров запускаются до exit 0.
- **V8** — `make ci` зелёный; diff-coverage M1 96.2% / M2 98.3% / M3 96.9%
  (≥95% на затронутых файлах).

### 7.3 Где реальность разошлась с черновиком §3

- **`ParameterOption` безошибочный** (`func(*Parameter)`), а не форма
  `options.Option`, которую подразумевала проза FR-1. Опции-флаги не могут упасть,
  так что возврат ошибки был бы непокрываемой веткой (урок SRD-008); более простая
  сигнатура честна и полностью покрыта.
- **Гейты читают *определения* IoSpec / dataInputs, а не frame-инстансы.**
  `scope.Frame.instantiateParams` пересобирает параметры только из name +
  `ItemAwareElement`, так что per-execution-инстансы не несут флагов
  optional/while-executing; поэтому `data.RequiredItemIDs` идёт по определениям
  (`IoSpec.InputSet()`/`OutputSet()`, `throwEvent.dataInputs`).
- **Доступность — на основе состояния, а не association.** Required-вход, заранее
  посеянный `Ready` своим определением, проходит start-gate без association; гейт
  проверяет состояние frame-инстанса, что есть корректная BPMN-семантика
  «доступен».
- **Post-check у `throwEvent.LoadData` вынесен** в `missingRequiredInputs`, чтобы
  удержать функцию в бюджете gocyclo после добавления гейта.

## 8. Ссылки

- [ADR-011 v.2 Process Data Flow](../design/ADR-011-process-data-flow.md) — §2.2
  (один набор, optional/required/while-executing на параметре), §2.3 (availability
  гейтит старт, никогда не ждёт), §2.4–§2.5 (associations, events), §2.7 (нет типа
  `Set`). Этот SRD их приземляет; отложенные пункты (while-executing-рантайм,
  service reader, multi-set) названы в §2.2.
- [ADR-010 v.1 Process Data Model](../design/ADR-010-process-data-model.md) —
  машинерия execution frame / association / commit, на которой сидят эти гейты.
- [SRD-007 v.1 Process Data Model](SRD-007-process-data-model.md) — приземление,
  построившее путь frame/association/load-commit, который этот SRD переиспользует.
- [SRD-008 v.1 Data model-layer hardening](SRD-008-data-model-hardening.md) —
  предыдущий data-слой-SRD; его единоличный граф `Parameter`↔`Set` замещён здесь
  удалением `Set` (ADR-011 v.2 §2.7).
- [SAD-001 v.1 §14.1](../design/SAD-001-vision-and-architecture.md) — отклонения
  no-wait и single-set, которые реализует этот SRD.

## 9. Открытые вопросы

- Нет. Форма API конструирования (`WithParameters` + опции параметра) и
  размещение гейтов (`LoadData`/`UploadData`) решены выше; рантайм-вычисление
  `whileExecuting`, service reader и multi-set отложены с названными домами (§2.2).
  Приземляется ли M1 атомарно или expand→contract — деталь milestone-plan, а не
  концептуальный вопрос.

## История документа

| Версия | Дата | Автор | Изменение |
|---|---|---|---|
| v.1 | 2026-06-13 | Руслан Габитов | **Принято**, приземлено на `feat/io-set-evaluation` (M1 `75a8105`, M2 `8530583`, M3 `0296ab4`, после правки ADR-011 v.2 `89ca4fd`); `make ci` зелёный, diff-coverage ≥95% на каждом milestone; все пять примеров запускаются. §7 заполнена — см. §7.3 про расхождения (безошибочный `ParameterOption`; гейты читают определения, а не frame-инстансы; доступность на основе состояния; вынесенный post-check `throwEvent`). Приземляет ADR-011 v.2 §2.2–§2.5 + §2.7 «нет типа `Set`»: отбрасывает реифицированный `Set`/`SetType` (вкл. мёртвые `Link`/`linkedSets`), делает required/optional/while-executing атрибутами на `Parameter`, заставляет `IoSpec` владеть параметрами напрямую с views `InputSet()`/`OutputSet()`, заменяет конструирование `WithSet`/`WithEmptySet` опциями с помеченными параметрами и добавляет рантайм start-gate (required-входы доступны иначе fail-fast-ошибка, нет ожидания; optional может отсутствовать) и completion-gate (required-выходы произведены иначе ошибка). Рантайм-вычисление `whileExecuting`, service reader и множественные I/O sets отложены. Три milestone'а (отбросить Set → start-gate → completion-gate). Реализует ADR-011 v.2; уточняет ADR-010 v.1. |
