# SRD-034 — Исполнение UserTask как узла ожидания, авторизация human-task, ManualTask

| Поле | Значение |
|---|---|
| Статус | Принято |
| Версия | v.1 |
| Дата | 2026-07-03 |
| Владелец | Руслан Габитов |
| Реализует | [ADR-020 v.1 Human-Interaction Execution Model](../design/ADR-020-human-interaction-execution-model.md) |

Приземляет [ADR-020 v.1](../design/ADR-020-human-interaction-execution-model.md) на ветке
`feat/human-interaction-model`: переделывает **UserTask** в узел ожидания на существующем ядре
park/resume (без нового механизма pause/resume), добавляет **триадную авторизацию** в стиле Camunda
(`assignee` / `candidateUsers` / `candidateGroups`), применяемую как на чтении (`Take`), так и на
записи (`Complete`), приземляет **ManualTask** как no-op-транзит, чинит дефект множественности
рендереров и поставляет «из коробки» **консольные** `TaskDistributor` + рендерер с исполнимым
примером. Закрывает находку аудита **AB-002**.

## 1. Предпосылки (проверено по коду)

### 1.1 Дефекты (AB-002)

**Блокирующая активация / неотменяемый park (аудит §2.9).** `UserTask.Exec`
(`pkg/model/activities/user_task.go:176`) регистрирует канал рендерера и **блокируется** на нём:

```go
// user_task.go:200-202 — the blocking loop
for d := range rCh {
    dd = append(dd, d)
}
```

`rCh` приходит из `re.RenderRegistrator().Register(ut)`. Цикл **игнорирует `ctx`**, так что ждущий
UserTask нельзя отменить — при аборте инстанса или прерывающем boundary его track-горутина остаётся
заблокированной навсегда, и это обходит дисциплину single-writer цикла инстанса. Любой другой узел
ожидания кооперативно паркуется на канале, питаемом циклом; UserTask — нет.

**Множественность рендереров (аудит §2.8).** `WithRenderer` (`user_task_options.go:66`) дедуплицирует
по:

```go
return r2c.ID() == r.ID() || r2c.Implementation() == r.Implementation()
```

Клауза `Implementation()` отвергает второй рендерер того же *вида* реализации — но два рендерера
одного вида (например, две `##html`-формы) — это законно разные отрисовки (BPMN `Rendering`
повторяем). Различные рендереры должны различаться только по `ID()`.

**Нет рантайм-авторизации.** Модель `ResourceRole` объявлена (`activity.Roles()`
`pkg/model/activities/activity.go:121`), но никогда не вычисляется; нет рантайм-идентичности
действующего лица и нет проверки членства. Контракт интерактора (`pkg/interactor/interactor.go`) —
**только интерфейс**, без production-реализации, а `Registrator` передаётся **`nil`** в каждый инстанс
(`pkg/thresher/thresher.go:830`, `:977`).

**У ManualTask нет исполнения.** Нет `manual_task.go`; `flow.ManualTask` даже не объявлен как
`TaskType` (`pkg/model/flow/activity.go:26-36` перечисляет только Receive/Script/Send/Service/User).

### 1.2 Решение, которое приземляет этот SRD

[ADR-020 v.1](../design/ADR-020-human-interaction-execution-model.md) решает: UserTask — узел ожидания,
чьё завершение является внешним событием; подключаемая граница `TaskDistributor`; авторизуемые
entry-точки движка `Take`/`Complete`; триада Camunda, выраженная как фасад над `ResourceRole`,
резолвимый в момент авторизации (статический или `FormalExpression`); рантайм-идентичность `Actor`;
проверки `Authorizer` + `OutputValidator`, принадлежащие UserTask, с `Instance` в роли оркестратора;
типизированный возврат `TaskView`; множественность рендереров по идентичности; ManualTask как no-op.
Этот SRD сверяет ту концепцию с кодовой базой.

### 1.3 Рельсы, по которым должен ехать UserTask (существующий механизм)

- **Классификация узла ожидания** — `track.checkNodeType` (`internal/instance/track.go:337`) паркует
  узел тогда и только тогда, когда он одновременно `flow.EventNode` и `eventproc.EventProcessor`: он
  выставляет `updateState(TrackWaitForEvent)` (`:368`), эмитит `evWaiting` (`:377`) и регистрирует
  waiter на определение. UserTask сегодня ни то ни другое, поэтому он пропускается → блокирующий путь.
- **Кооперативный park** — припаркованный трек блокируется в `track.run` на
  `select { <-ctx.Done(); <-t.evtCh }` (`track.go:492-513`); цикл инстанса — **единственный
  sender/closer** `evtCh` (`evtCh chan flow.EventDefinition`, `:186`). Ноль CPU, отменяемо.
- **Доставка** — сработавший триггер доходит до цикла как `evDeliver` trackEvent
  (`internal/instance/event.go:92`); цикл маршрутизирует его в `evtCh` припаркованного трека; трек
  просыпается и выполняет `deliver` → `ep.ProcessEvent(ctx, eDef)` на своей горутине (`track.go:968+`).
- **Резолв выражений** — `expression.Engine.Evaluate(ctx, expr data.FormalExpression, src data.Source)`
  (`pkg/model/expression/expression.go:21`) над `data.Source` (`Find(ctx, name) (Data, error)`,
  `pkg/model/data/data.go:29`); scope инстанса уже представлен как `data.Source` через `execEnv.Find`
  (`internal/instance/execenv.go`), и корреляция резолвится так же (`msgflow` `payloadSource`,
  `pkg/model/msgflow/correlation.go:27`).
- **Паттерн опции движка** — `WithMessageBroker` (`pkg/thresher/options.go:125`) валидирует non-nil,
  задаёт поле `thresherConfig` (`:27`), читаемое через accessor (`:215`), с дефолтом в `defaultConfig`
  (`:228`, который ставит `membroker.New()` на `:235`). `WithTaskDistributor` повторяет это в точности.

## 2. Требования

### Функциональные

- **FR-1 — UserTask паркуется как узел ожидания.** `checkNodeType` получает ветку UserTask: переход в
  `TrackWaitForEvent`, park горутины на `evtCh`, анонс задачи в `TaskDistributor` и регистрация
  припаркованной задачи в индексе задач движка по **task id**, чтобы `Take`/`Complete` маршрутизировались
  обратно к ней. Блокирующий путь `Exec`/`Registrator` удаляется. Горутина удерживается в памяти (как и
  все виды ожидания) и кооперативно отменяема через `ctx.Done()` / закрытие `evtCh`.
- **FR-2 — рантайм-идентичность `Actor`.** Интерфейс `hi.Actor` (`UserID() string`, `Groups() []string`)
  в `pkg/model/hinteraction` — отличный от BPMN-элемента `hi.Performer` (`resources.go:133`).
- **FR-3 — опции объявления триады.** Шесть опций на UserTask, строящих `ResourceRole`
  (`PotentialOwner`/`HumanPerformer`) через существующий путь `AddRole`: `WithAssignee(id)` /
  `WithAssigneeExpr(expr)`, `WithCandidateUsers(...id)` / `WithCandidateUsersExpr(expr)`,
  `WithCandidateGroups(...id)` / `WithCandidateGroupsExpr(expr)`. Статическая и выражательная формы для
  одного участника взаимоисключающи (страж resource-XOR-expression в `NewResourceRole`,
  `resources.go:53`).
- **FR-4 — `Authorizer` на UserTask.** `Authorize(ctx, actor Actor, src data.Source, eng expression.Engine) error`:
  резолвит каждого участника триады (статическое множество или вычисляет его `FormalExpression` над
  `src` → `[]string`), затем решает членство — задан `assignee` ⇒ `actor.UserID ∈ assignee`; иначе
  `actor.UserID ∈ candidateUsers` **или** `actor.Groups ∩ candidateGroups ≠ ∅`; ни один участник не
  объявлен ⇒ авторизован (открыто). Проваленное выражение резолвится в пустое множество (BPMN:
  проваленный запрос ресурса ⇒ пусто), т.е. не авторизует никого.
- **FR-5 — `OutputValidator` на UserTask.** `ValidateOutputs(outputs []data.Data) error`: каждый
  обязательный параметр `Outputs()` (`user_task.go:126` → `[]*bpmncommon.ResourceParameter`, каждый с
  `Name()`/`Type()`/`IsRequired()`, `resource.go:135-145`) присутствует и типово-конформен;
  неизвестные/лишние выходы отвергаются.
- **FR-6 — `Take`.** `Take(ctx, taskID string, actor Actor) (TaskView, error)`, обслуживаемый циклом
  инстанса (request/reply, §4.1): авторизует `actor`; при успехе делает снапшот и возвращает `TaskView`;
  при провале возвращает ошибку и **не** раскрывает данные. Токен не возобновляет.
- **FR-7 — `Complete`.** `Complete(ctx, taskID string, actor Actor, outputs []data.Data) error`,
  обслуживаемый циклом: авторизация → `ValidateOutputs`; при успехе биндит выходы и возобновляет
  припаркованный трек (через синтетическое событие завершения на `evtCh`), отвечает `nil`; при провале
  отвечает ошибкой и оставляет задачу **припаркованной** (нетерминально — ждёт правильного actor'а /
  исправленных выходов).
- **FR-8 — `TaskView` / `TaskInfo` / `TaskRef`.** Общий встроенный `TaskRef` (четыре id); `TaskInfo`
  (анонс, `Distribute`) = `TaskRef` + `Roles`, **без данных**; `TaskView` (авторизованное чтение,
  `Take`) = `TaskRef` + `Renderers` + `Data`. `Data` несёт входные данные задачи **и** её `Property`
  (`Property` — это `data.Data`, `property.go:178`); `FORM_ID` — userland-конвенция `Property`, не поле
  движка. Отсутствие поля `Data` в `TaskInfo` делает границу до авторизации compile-time.
- **FR-9 — граница `TaskDistributor` + опция.** Интерфейс `{ Distribute(ctx, TaskInfo) error;
  Withdraw(ctx, taskID string) error }`; опция движка `WithTaskDistributor(d)`, повторяющая
  `WithMessageBroker` (поле конфига + non-nil-страж + accessor). Опционально: если не задан, задачи
  всё равно паркуются и завершаемы по id (без анонса распределения).
- **FR-10 — множественность рендереров по идентичности.** `WithRenderer` дедуплицирует только по `ID()`
  (убрать клаузу `Implementation()`, `user_task_options.go:66`).
- **FR-11 — ManualTask no-op.** Добавить TaskType `flow.ManualTask` (`flow/activity.go`); новый
  `pkg/model/activities/manual_task.go`, чей `Exec` немедленно возвращает `Outgoing()` (без дескриптора,
  без ожидания) — неоперациональный транзит BPMN §13.1.
- **FR-12 — консольное взаимодействие «из коробки» + пример.** `pkg/interactor/console`: консольный
  `hi.Renderer` и консольный `TaskDistributor`; исполнимый `examples/usertask/`, который строит процесс
  с одним UserTask, подключает консольный distributor, программно драйвит `Take`/`Complete`
  (фиксированные actor + выходы), печатает консольное представление и выходит с кодом 0.

### Нефункциональные

- **NFR-1 — single-writer сохранён.** Все чтения/записи scope и возобновление токена для `Take`/`Complete`
  происходят на горутине цикла инстанса (request/reply, §4.1). Никаких мутаций scope с чужой горутины.
- **NFR-2 — нет утечки горутин.** Припаркованный UserTask отменяем через `ctx.Done()` / закрытие
  `evtCh` (`track.go:492`); прерывающий boundary
  ([ADR-018 v.1](../design/ADR-018-boundary-events-and-activity-interruption.md)) сносит припаркованный
  waiter и делает `Withdraw` задачи.
- **NFR-3 — никакого нового механизма pause/resume.** Переиспользуются `TrackWaitForEvent` / `evtCh` /
  `evDeliver`; завершение UserTask — ещё один вид события, текущий через ядро ADR-017.
- **NFR-4 — слоистость.** `pkg/model/activities` самопроверяется, используя только абстракции
  model-слоя (`data.Source`, `expression.Engine`, `hi.Actor`); он не должен импортировать `internal/`.
- **NFR-5 — наблюдаемость.** Эмитить сигналы жизненного цикла задачи (`distributed` / `taken` /
  `completion.rejected` с причиной / `completed` / `withdrawn`) через канал инстанса
  ([ADR-013 v.1](../design/ADR-013-instance-observability.md)); никогда не логировать полезную нагрузку
  задачи.
- **NFR-6 — покрытие.** Затронутые файлы финишируют с diff-coverage ≥95% (цель 100%) под `make ci`
  (`COVER_MIN=95`, `Makefile:15`).

## 3. Модели

### 3.1 `hi.Actor` (`pkg/model/hinteraction`)

```go
// Actor is the authenticated human acting on a task — the runtime identity the
// TaskDistributor supplies and the engine authorizes. Distinct from the BPMN
// Performer element (a ResourceRole subtype).
type Actor interface {
    UserID() string
    Groups() []string
}
```

### 3.2 Триада + `Authorizer`/`OutputValidator` (`pkg/model/activities`, `pkg/model/hinteraction`)

Стандартный `hi.ResourceRole` держит одну ссылку `Resource` **или** `assignmentExpression` — он не
может нести различие user/group триады, статические **списки** id или маркер слота. Поэтому триада —
это **типизированная структура** (`pkg/model/hinteraction`), единственный источник истины,
выставляемый через типизированный accessor и читаемый `Authorize`; она **сосуществует** с
дженериковым `Roles()`, а не проецируется в него (§4.3):

```go
// hinteraction — one triad member: static identifiers XOR an expression → a set.
type AssignmentSlot int // Assignee | CandidateUsers | CandidateGroups
type Assignment struct {
    slot   AssignmentSlot
    static []string              // XOR expr
    expr   data.FormalExpression
}
```

UserTask держит до одного `Assignment` на слот (приватные поля) и реализует оба check-интерфейса;
`Authorize` читает типизированные `Assignment` напрямую (не перепарся `Roles()`):

```go
type Authorizer interface {
    Authorize(ctx context.Context, actor hi.Actor, src data.Source, eng expression.Engine) error
}
type OutputValidator interface {
    ValidateOutputs(outputs []data.Data) error
}
```

### 3.3 `TaskRef`, `TaskInfo`, `TaskView`, `TaskDistributor` (граница движка)

`TaskInfo` (анонс) и `TaskView` (авторизованное чтение) **различны по жизненному циклу**, а не
дубликаты: `TaskInfo` передаётся в `Distribute` в момент park'а — *до* какой-либо авторизации — так
что он **не** должен нести `Data` задачи; `TaskView` возвращается `Take` *после* авторизации, поэтому
несёт. Общая идентичность вынесена во встроенный `TaskRef`, а различающиеся поля кодируют границу — тип
анонса без поля `Data` означает, что переменные инстанса не могут достичь distributor'а по построению.
`Renderers` живут только на `TaskView`: distributor получает форму, вызывая `Take` от имени человека,
а не из анонса.

```go
// TaskRef identifies a parked task across the boundary (embedded in both types).
type TaskRef struct {
    TaskID, InstanceID, NodeID, ProcessID string
}

// TaskInfo — the pre-authorization announcement handed to Distribute: identity +
// who may claim (for inbox routing/filtering). No data, no form.
type TaskInfo struct {
    TaskRef
    Roles []*hi.ResourceRole
}

// TaskView — the post-authorization snapshot returned by Take: the form to render
// and the self-describing data, for an actor who has passed authorization.
type TaskView struct {
    TaskRef
    Renderers []hi.Renderer
    Data      []data.Data
}

type TaskDistributor interface {
    Distribute(ctx context.Context, task TaskInfo) error
    Withdraw(ctx context.Context, taskID string) error
}
```

`Take`/`Complete` — методы движка (на Thresher), маршрутизируемые по `taskID` через индекс задач движка
к владеющему циклу инстанса.

### 3.4 ManualTask (`pkg/model/activities/manual_task.go`)

```go
type ManualTask struct{ task }
func (mt *ManualTask) Exec(_ context.Context, _ renv.RuntimeEnvironment) ([]*flow.SequenceFlow, error) {
    return mt.Outgoing(), nil // BPMN §13.1 non-operational — pass-through
}
func (mt *ManualTask) TaskType() flow.TaskType { return flow.ManualTask }
```

### 3.5 Консольная референсная реализация (`pkg/interactor/console`)

`Renderer`, который печатает свои поля и возвращает собранные `data.Data`, и `TaskDistributor`, который
печатает `TaskInfo` на `Distribute` (и отрисовывает `TaskView`, когда драйвит `Take`) и очищает на
`Withdraw`. Референсного/проверочного качества; не zero-config-дефолт.

## 4. Анализ

### 4.1 `Take`/`Complete` — request/reply, обслуживаемый циклом (решено)

`Complete` должен вернуть синхронный вердикт, но весь доступ к scope и возобновление токена обязаны
оставаться на горутине цикла инстанса (single-writer из ADR-017). Поэтому entry-точки движка ставят в
очередь запрос с reply-каналом на цикл целевого инстанса (новые входящие виды trackEvent, рядом с
`evDeliver`). Цикл выполняет `UserTask.Authorize` (над `data.Source` из `execEnv` инстанса) и, для
`Complete`, `ValidateOutputs`, затем отвечает вердикт; при успешном `Complete` он доставляет
синтетическое событие завершения (несущее выходы) в `evtCh` припаркованного трека, так что трек биндит
выходы в своём `ProcessEvent` и продвигается — ровно путь message-catch. **Отвергнуто:** выполнять
проверки на горутине вызывающего — это гонит scope против цикла и нарушает single-writer.

Проработанный поток (Complete; Take — тот же минус validate/bind/resume):

```
1. Thresher.Complete(ctx, taskID, actor, outputs)
     → look up taskID in the engine task index → the owning instance
     → enqueue completeReq{actor, outputs, replyCh} onto that instance's loop; block on replyCh
2. Instance loop (single-writer goroutine) dequeues completeReq, resolves the parked UserTask node:
     a. task.Authorize(ctx, actor, execEnv /*data.Source*/, engine)
          → for each triad member: static set, or engine.Evaluate(expr, src) → []string
          → membership verdict (assignee-restrictive / candidate / open)
          → non-nil error ⇒ reply err on replyCh; task stays parked; STOP
     b. task.ValidateOutputs(outputs)  → non-nil error ⇒ reply err; task stays parked; STOP
     c. emit a synthetic completion event (carrying outputs) to the parked track's evtCh
     d. reply nil on replyCh
3. Parked track wakes from <-t.evtCh → deliver → UserTask.ProcessEvent binds outputs to scope,
     Withdraw(taskID), advances the token onto Outgoing()
4. Thresher.Complete returns the replyCh verdict (nil on success)
```

### 4.2 Ветка узла ожидания UserTask, а не синтетический `EventNode` (решено)

UserTask — не BPMN-событие, поэтому он не должен маскироваться под `flow.EventNode`. `checkNodeType`
получает явную ветку UserTask, которая паркует трек и регистрирует его в индексе задач движка (по task
id) вместо регистрации хаб-waiter'ов. Завершение всё равно едет через `evtCh` как синтетический
`flow.EventDefinition`, так что путь wake/deliver неизменен. **Отвергнуто:** дать UserTask фальшивое
message/signal-определение — это загрязнило бы корреляцию и event-хаб не-событием.

### 4.3 Типизированная триада, выставленная через типизированный accessor (решено; уточняет ADR-020 §2.5)

Grounding показал, что `hi.ResourceRole` не может нативно нести триаду (одна ссылка `Resource` XOR
выражение; нет различия user/group, нет статического списка id, нет слота). Поэтому **типизированный
`Assignment`** (§3.2) — единственный источник истины: шесть опций задают per-slot `Assignment` у
UserTask, `Authorize` читает их напрямую, а UserTask выставляет их через типизированный accessor
(`Assignments()`). Триада **сосуществует** с дженериковым `Roles()` (`WithRoles`) — она **не**
проецируется в него: проекция была бы с потерями (статические списки id схлопываются в одиночные роли
`Resource`; слот/вид непредставим), и сама Camunda держит триаду в extension-атрибутах, отдельно от
BPMN `ResourceRole`. **Отвергнуто:** хранить/проецировать триаду *как* дженериковые `ResourceRole`.

### 4.4 Проверки живут на UserTask; Instance оркестрирует (решено, из ADR-020)

`Authorizer`/`OutputValidator` — на UserTask (он объявляет триаду + спецификацию выходов). Цикл лишь
оркестрирует `authorize → validate → bind → resume`; `TaskDistributor` не держит check-логики.
Держит `pkg/model/activities` свободным от импортов `internal/` (NFR-4).

### 4.5 Встроенная консольная реализация, opt-in; zero-config-дефолт остаётся опциональным (решено)

Ни один встроенный дефолт не может авто-завершить UserTask (для завершения нужен человек/драйвер),
поэтому консольный I/O не должен быть молчаливым дефолтом. Консольный `TaskDistributor` — opt-in
(`WithTaskDistributor(console.New())`); при отсутствии любого distributor'а задачи всё равно паркуются
и завершаемы по id (FR-9). Это референс «из коробки» + smoke-артефакт, повторяющий `membroker` как
встроенную-но-явную реализацию.

### 4.6 Что остаётся без изменений (решено)

Accessor'ы `Outputs()` / `Renderers()`, объектная модель `ResourceRole`, машинерия `evtCh`/`evDeliver`
и single-writer цикл инстанса переиспользуются без изменений. Меняются только путь активации UserTask и
поверхность опций/авторизации.

## 5. API / поверхность контракта

- Методы движка (Thresher): `Take(ctx, taskID, actor) (TaskView, error)`, `Complete(ctx, taskID, actor,
  outputs) error`.
- Опция движка: `WithTaskDistributor(TaskDistributor) Option`.
- Опции модели: шесть триадных `WithX`/`WithXExpr`; `WithRenderer` (дедуп по ID); UserTask теперь
  удовлетворяет `Authorizer` + `OutputValidator`.
- Новые элементы: `hi.Actor`, `TaskRef`, `TaskInfo`, `TaskView`, `TaskDistributor`, `activities.ManualTask`,
  `flow.ManualTask`, `pkg/interactor/console`.

## 6. Сценарии тестов

- **FR-2/3/4** `TestUserTaskAuthorize` — assignee-ограничивающий; совпадение candidateUsers; пересечение
  candidateGroups; открытый без триады; резолв кандидатского множества выражением (над `data.Source`);
  проваленное выражение ⇒ отказ.
- **FR-5** `TestUserTaskValidateOutputs` — обязательный присутствует/отсутствует; несоответствие типа;
  лишний выход отвергнут.
- **FR-1/6/7** `TestUserTaskParkTakeComplete` — паркуется (нет горутины, заблокированной на чужом канале);
  `Take` неавторизован ⇒ ошибка + нет данных; авторизован ⇒ `TaskView` с id+рендереры+данные; `Complete`
  неавторизован ⇒ нетерминально (всё ещё припаркован); невалидные выходы ⇒ нетерминально; валидные ⇒
  биндит + возобновляет на `Outgoing()`.
- **NFR-2** `TestUserTaskCancelWhileParked` — отмена `ctx` / прерывающий boundary сносит припаркованную
  задачу и делает её `Withdraw`; нет утёкшей горутины.
- **FR-9** `TestWithTaskDistributorNil` — отвергает nil; `TestDistributeWithdraw` — анонс на park'е,
  withdraw на complete/cancel.
- **FR-10** `TestWithRendererDedupByID` — два рендерера с одним `Implementation()`, но разным `ID()` оба
  сохранены; тот же `ID()` отвергнут.
- **FR-11** `TestManualTaskPassThrough` — токен течёт прямо на `Outgoing()`, без ожидания.
- **FR-12** консоль: `TestConsoleRendererCollects`, `TestConsoleDistributor`; `examples/usertask/`
  исполняется до exit 0 под таймаутом (smoke).

## 7. Вехи

1. **M1 — `Actor` + триадная авторизация (model-слой).** `hi.Actor`; шесть триадных опций;
   `UserTask.Authorize` (статика + `FormalExpression`); `UserTask.ValidateOutputs`. Юнит-тестируется
   изолированно.
2. **M2 — редизайн UserTask как узла ожидания.** Ветка UserTask в `checkNodeType`; индекс задач движка;
   `Take`/`Complete` request/reply на цикле + синтетическое событие завершения; `TaskView`; интерфейс
   `TaskDistributor` + `WithTaskDistributor`; удаление блокирующих `Exec`/`Registrator`.
3. **M3 — фикс множественности рендереров.** `WithRenderer` дедуп по `ID()`.
4. **M4 — ManualTask no-op.** `flow.ManualTask` + `manual_task.go` транзит.
5. **M5 — консольная реализация + пример.** `pkg/interactor/console` рендерер + distributor; исполнимый
   `examples/usertask/` (smoke-able).

## 8. Cross-doc

- **Реализует** [ADR-020 v.1](../design/ADR-020-human-interaction-execution-model.md).
- Ссылки (вверх/вбок): [ADR-001 v.6](../design/ADR-001-execution-model.md),
  [ADR-006 v.2](../design/ADR-006-events-and-subscriptions.md),
  [ADR-010 v.2](../design/ADR-010-process-data-model.md),
  [ADR-011 v.5](../design/ADR-011-process-data-flow.md),
  [ADR-013 v.1](../design/ADR-013-instance-observability.md),
  [ADR-017 v.1](../design/ADR-017-channel-based-event-processing.md),
  [ADR-018 v.1](../design/ADR-018-boundary-events-and-activity-interruption.md).
- Направление — SRD → ADR (вверх); нисходящих ссылок нет. В любой будущей цитате этот SRD указывается
  только номером.

## 9. Definition of Done

- FR-1…FR-12 реализованы и подключены; NFR-1…NFR-6 соблюдены.
- Каждый FR/NFR покрыт ≥1 именованным тестом §6, все зелёные под `-race`.
- Блокирующий путь `Exec` и nil-обвязка `Registrator` удалены.
- `make ci` зелёный (tidy · lint · build · `-race` · diff-coverage ≥95% на затронутых файлах ·
  govulncheck).
- `examples/usertask/` исполняется до exit 0 под таймаутом; его собранный бинарник в gitignore.
- ADR-020 (+ RU-твин) и SRD-034 переводятся в Accepted; связанные доки синхронизированы (audit backlog
  AB-002, backlog).

## 10. Итог реализации

Приземлено на `feat/human-interaction-model` пятью вехами; `make ci` зелёный
(diff-coverage 96.9% ≥ 95% gate, `-race` чист, lint чист, govulncheck чист,
smoke `examples/usertask/` выходит 0).

| Веха | Коммит | Объём |
|---|---|---|
| M1 | `e322113` | `hi.Actor`, `hi.Assignment` (слот + статический/выражательный `Resolve`), шесть триадных опций, `UserTask.Authorize`/`ValidateOutputs`/`Assignments()`. |
| M2a | `2d6fb0a` | `interactor.TaskRef`/`TaskInfo`/`TaskView`/`TaskDistributor`/`NopDistributor`; `thresher.WithTaskDistributor` + поле конфига/accessor/дефолт. |
| M2b | `7e01999` | UserTask как узел ожидания (ветка `checkNodeType`, `parkHumanTask`, синтетический `TaskCompletion`, переписанные `Exec`/`ProcessEvent`); `inst.taskReq` request/reply на цикле + `Take`/`Complete`; реестр Thresher + маршрутизирующий distributor + `Take`/`Complete`; удалены блокирующий `Exec`, nil `Registrator` и мёртвые `interactor.{Interactor,Registrator,RenderController}` / `renv.RenderRegistrator` / `hi.{Performer,HumanPerformer}` / `mockinteractor`. |
| M3 | `d525d60` | `WithRenderer` дедуп только по `ID()` (§2.8). |
| M4 | `0a3e2ea` | `flow.ManualTask` + `activities.ManualTask` no-op транзит. |
| M5 | `3cb2268` | `pkg/interactor/console.Driver` (авто-драйвит через `consinp`) + исполнимый `examples/usertask/`. |

**Уточнения, свёрнутые в доки по ходу реализации:** триада — типизированная
структура, выставляемая через `Assignments()`, **сосуществующая** с `Roles()`, а не
проецируемая в него (§3.2/§4.3, ADR-020 §2.5) — проекция с потерями (нет различия
user/group, нет статического списка id, нет слота); и M5 **переиспользует** существующий
консольный рендерер `consinp`, а не добавляет новый (FR-12).

## Открытые вопросы

Нет.
