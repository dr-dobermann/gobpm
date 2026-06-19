# SRD-021 — Разделение Exclusive- и Inclusive-шлюзов (маршрутизация на основе данных)

| Поле | Значение |
|---|---|
| Статус | Принято |
| Версия | v.1 |
| Дата | 2026-06-19 |
| Владелец | Руслан Габитов |
| Реализует | [ADR-005 v.2 Gateways & Joins](../design/ADR-005-gateways-and-joins.ru.md) §2.8, §2.9 |

Этот SRD приземляет **разделения маршрутизации на основе данных**, решённые в
[ADR-005 v.2](../design/ADR-005-gateways-and-joins.ru.md): **Exclusive (XOR)
split** (§2.8 — first-true / default / исключение) и **Inclusive (OR) split**
(§2.9 — разветвление по истинному подмножеству / default / исключение), с
вычислением условных и default-потоков последовательности **на шлюзах**.
**Inclusive OR-join** (§2.10) — это родственный SRD-022 (в своей ветке);
Exclusive merge уже является несинхронизирующим pass-through (ADR-005 §2.3/§2.7).
**Условные потоки на уровне задачи** (issue #51 — собственный исходящий поток
задачи, маршрутизируемый по условию) остаются вне scope: ADR-005 §2.7 по-прежнему
разветвляет исходящие потоки задачи безусловно; #51 — отдельная работа, которая
переиспользует helper условий из этого SRD.

## 1. Контекст и мотивация

### 1.1 Текущее состояние (проверено по коду)

- **Exclusive split существует, но отклоняется от §2.8 и не задействован.**
  `ExclusiveGateway.Exec` (`pkg/model/gateways/exclusive.go:69-117`) вычисляет
  условие каждого исходящего потока через `checkCondition`
  (`exclusive.go:123-150`, через `re.ExpressionEngine().Evaluate`), откатывается к
  `defaultFlow`, когда ни одно не совпало (`:94-104`), и возвращает ошибку, когда
  default отсутствует (`:96-101`). Два отклонения от §2.8 / §13.4.2:
  - Он **собирает каждое истинное условие и выдаёт ошибку, если истинно более
    одного** (`:106-114`) — но стандарт это **first-true, с короткой замыканием**
    («первое условие, которое вычисляется в true … больше условий не
    вычисляется», §13.4.2); перекрытие не является ошибкой.
  - Он **не учитывает направление**: *сходящийся* Exclusive merge (единственный
    исходящий поток без условия) перебирает этот исходящий, пропускает его
    (nil-условие → `continue`, `:78-80`), не находит потоков и **падает с "no
    available outgoing flows"** (`:94-101`). Чистый XOR merge сегодня сломан.
- **Он подключён, но не покрыт сквозными тестами.** `Exec` расходящегося шлюза
  вызывается из `track.executeNode` → `checkFlows`
  (`internal/instance/track.go:507-547`, `:610-666`), который продолжает по одному
  возвращённому потоку и разветвляет остальные (`evFork`). `ExclusiveGateway`
  используется **только** в `internal/instance/snapshot/snapshot_test.go` —
  **нет** упражнения XOR на уровне движка / примера; `exclusive_test.go`
  юнит-тестирует `Exec` только для случаев no-outgoing / with-default / normal (без
  перекрытия, без merge).
- **Inclusive-шлюза нет.** В `pkg/model/gateways/` есть `gateway.go`
  (база: `direction`, `defaultFlow`, `DefaultFlowHolder`), `exclusive.go`,
  `parallel.go` — **нет `inclusive.go`**. У OR split нет ни модели, ни рантайма.
- **Условия + default-потоки смоделированы, но ещё не задействованы
  маршрутизацией.** Потоки последовательности несут
  `conditionExpression data.FormalExpression`
  (`pkg/model/flow/sequenceflow.go:50`, `Condition()` `:295-297`,
  `flow.WithCondition`); шлюзы держат default через `DefaultFlowHolder`
  (`gateway.go:151-193`). Вычисление этих условий шлюзами — это то, что
  доставляет данный SRD; условная маршрутизация на уровне задачи (#51) отдельна
  (задачи по-прежнему разветвляют все исходящие — каждый `Exec` задачи возвращает
  `Outgoing()` безусловно, напр. `service_task.go:177`, по ADR-005 §2.7).

### 1.2 Проблема

Процесс не может ветвиться на данных: split XOR падает на merge и на перекрытии
условий, никогда не прогоняется через движок, а у OR вообще нет шлюза. Этот SRD
приводит Exclusive split в соответствие с §2.8, добавляет Inclusive split (§2.9)
и доказывает оба сквозными тестами.

## 2. Решение

- **Exclusive split → §2.8.** Сделать `ExclusiveGateway.Exec` **first-true, с
  коротким замыканием** (взять первый исходящий, чьё условие истинно, и
  остановиться), использовать **default**, когда ни одно не совпало, и **завалить
  экземпляр** только когда ни одно не совпало *и* default отсутствует — заменяя
  логику collect-all / error-on-overlap.
- **`Exec`, учитывающий направление.** Расходящийся шлюз (более одного исходящего)
  выбирает по условию; **pass-through** шлюз (сходящийся merge / единственный
  исходящий) возвращает свои исходящие потоки безусловно — без вычисления условий,
  без ошибки "no available outgoing flows". Это чинит XOR merge.
- **Inclusive split (новый `InclusiveGateway`).** Новый тип шлюза, зеркалящий
  `ExclusiveGateway`, чей расходящийся `Exec` возвращает **подмножество исходящих
  потоков, чьи условия истинны** (≥1), откатываясь к default, заваливаясь, когда
  ни одно + нет default (§2.9). `checkFlows` разветвляет это подмножество ровно как
  Parallel split.
- **Общее вычисление условий.** Helper вычисления условий (`checkCondition`)
  переезжает в базовый `Gateway`, чтобы Exclusive и Inclusive разделяли одно
  вычисление на основе ExpressionEngine с типом bool (без дублирования).
- **OR-join вне scope (SRD-022).** Этот SRD строит только **split**
  Inclusive-шлюза. *Сходящийся* Inclusive-шлюз (OR-join, ADR-005 §2.10) требует
  машинерии reachability + повторного вычисления из SRD-022; пока она не
  приземлится, сходящийся Inclusive-шлюз не поддерживается (задокументировано, а не
  тихо неправильно смержено).

## 3. Функциональные требования

- **FR-1 — Exclusive first-true split.** `ExclusiveGateway.Exec` возвращает
  **первый** исходящий поток, чьё условие вычисляется в `true`, и не вычисляет
  дальнейшие условия (§2.8 / §13.4.2). Перекрывающиеся истинные условия — **не**
  ошибка (прежний путь ошибки `>1` удалён).
- **FR-2 — pass-through с учётом направления.** Когда шлюз не расходящийся
  (сходящийся merge или единственный исходящий поток), `Exec` возвращает свои
  исходящие потоки безусловно — несинхронизирующий pass-through (§2.3) — вместо
  падения на исходящем без условия.
- **FR-3 — default + исключение (Exclusive).** Когда ни одно условие не истинно,
  берётся **default**-поток; когда ни одно условие не истинно и default
  отсутствует, шлюз заваливает экземпляр классифицированной ошибкой
  (§2.8 / §13.4.2).
- **FR-4 — Inclusive split.** Новый `InclusiveGateway`
  (`pkg/model/gateways/inclusive.go`) реализует `exec.NodeExecutor`; его
  расходящийся `Exec` возвращает **каждый** исходящий поток, чьё условие `true`
  (истинное подмножество, ≥1), с тем же default / exception-fallback, что и FR-3
  (§2.9). `checkFlows` разветвляет подмножество.
- **FR-5 — общее вычисление условий.** Вычисление условий (с типом bool, через
  `re.ExpressionEngine().Evaluate`) живёт единожды в базовом `Gateway` и
  используется обоими шлюзами; не-bool результат условия — классифицированная
  ошибка.
- **FR-6 — условные + default-потоки шлюза сквозь.** Процесс с исходящими
  потоками, несущими условия, и default-потоком на шлюзе маршрутизируется корректно
  через движок (не только в юнит-тесте модели). (Условные потоки на уровне задачи
  — #51 — вне scope; см. §1.1.)
- **FR-7 — Inclusive OR-join вне scope.** `InclusiveGateway` **не** реализует
  `exec.SynchronizingJoin` в этом SRD; сходящийся Inclusive-шлюз не поддерживается
  до SRD-022. Задокументировано на типе.

## 4. Нефункциональные требования

- **NFR-1 — маршрутизация, обоснованная стандартом.** Exclusive = ровно один
  выход (first-true / default), Inclusive = истинное подмножество (≥1) — по
  ADR-005 §2.8/§2.9, §13.4.2/§13.4.3.
- **NFR-2 — никакой новой машинерии разветвления.** Оба split'а кормят
  существующий путь `Exec → checkFlows → evFork` (ADR-005 §2.7) без изменений;
  единственное изменение — *какие* потоки возвращает `Exec`.
- **NFR-3 — покрытие.** Затронутые файлы финишируют ≥80% (цель 100%)
  diff-покрытия; `make ci` зелёный.

## 5. Анализ путей (альтернативы)

- **Привести `ExclusiveGateway.Exec` в соответствие (выбрано) vs новый
  exclusive-узел.** Выбрано: существующий `Exec` уже вычисляет условия + default;
  привести его к first-true + учёту направления — меньший объём и сохраняет один
  Exclusive-тип. Переписывание отвергнуто.
- **First-true с коротким замыканием (выбрано) vs error-on-overlap (текущее) vs
  take-all.** Выбрано: §13.4.2 явна — первое истинное побеждает, дальше не
  вычисляется; перекрытие — это выбор моделирования, который спека разрешает
  порядком, а не ошибкой движка. Отвергнута текущая ошибка (несоответствующая
  стандарту) и take-all (это Inclusive, не Exclusive).
- **Exec, учитывающий направление по количеству исходящих (выбрано) vs поле
  шлюза `direction` vs отдельный путь merge.** Выбрано: `>1 исходящий` →
  условный выбор; `≤1 исходящий` → pass-through — устойчиво без зависимости от
  установленности поля `direction`, и merge (1 исходящий) проходит насквозь
  чисто. Точный дискриминатор (количество vs `direction`) фиксируется на
  реализации.
- **Новый `InclusiveGateway`, зеркалящий `ExclusiveGateway` (выбрано) vs общий
  параметрический шлюз.** Выбрано: отдельный тип по ADR-005 §2.1 (поведение
  на тип, без центрального switch); общая часть (вычисление условий) вынесена в
  базу. Отвергнут параметрический "routing gateway" с флагом режима (скрытый
  switch).
- **OR-join в этом SRD (отвергнуто).** Его машинерия reachability + повторного
  вычисления (ADR-005 §2.10) существенна и заслуживает сфокусированного SRD-022;
  split полезен и тестируем независимо.

## 6. API и ключевые формы

```go
// pkg/model/gateways/inclusive.go (new) — mirrors ExclusiveGateway:
type InclusiveGateway struct{ Gateway }
func NewInclusiveGateway(opts ...options.Option) (*InclusiveGateway, error)
func (ig *InclusiveGateway) Exec(ctx, re) ([]*flow.SequenceFlow, error) // true subset / default / exception
func (ig *InclusiveGateway) Clone() flow.Node
func (ig *InclusiveGateway) Node() flow.Node
var _ exec.NodeExecutor = (*InclusiveGateway)(nil)   // NOT SynchronizingJoin (SRD-022)

// pkg/model/gateways/gateway.go — condition eval moves to the base:
func (g *Gateway) checkCondition(ctx, re, cond data.FormalExpression, of *flow.SequenceFlow) (bool, error)

// pkg/model/gateways/exclusive.go — Exec reconciled to §2.8 (first-true,
// short-circuit, direction-aware pass-through; default; exception).
```

Нет новой публичной поверхности движка; процессы создают шлюзы существующими
`NewExclusiveGateway` / новым `NewInclusiveGateway` + `flow.WithCondition` +
`UpdateDefaultFlow`.

## 7. План тестирования

- **`TestExclusiveSplitFirstTrue`** (юнит модели) — перекрывающиеся истинные
  условия → возвращается **первый** поток, а не ошибка (FR-1).
- **`TestExclusivePassThrough`** (юнит модели) — единственный исходящий без
  условия (merge) → возвращается безусловно, без ошибки (FR-2).
- **`TestExclusiveDefaultAndException`** (юнит модели) — ни одно не истинно →
  default; ни одно не истинно + нет default → классифицированная ошибка (FR-3).
- **`TestInclusiveSplitSubset`** (юнит модели) — несколько истинных → возвращаются
  **все** истинные потоки; ни одно не истинно → default; ни одно + нет default →
  ошибка (FR-4).
- **`TestInclusiveConvergingUnsupported`** — сходящийся Inclusive-шлюз
  отвергается/помечается (FR-7).
- **Уровень движка (`pkg/thresher` или `internal/instance`)** —
  `TestExclusiveRoutingEndToEnd` (процесс: start → XOR → {A cond true | B
  default} → end; истинная ветка выполняется, другая нет) и
  `TestInclusiveSplitEndToEnd` (start → OR-split → разветвляет истинное
  подмножество, ветки выполняются конкурентно и завершаются) — доказывая условную
  маршрутизацию шлюзов сквозь.
- Запускаемый `examples/exclusive-routing` (или расширение примера) smoke-прогон
  exit 0.

## 8. Кросс-документная согласованность

- **Реализует** [ADR-005 v.2](../design/ADR-005-gateways-and-joins.ru.md) §2.8
  (Exclusive split), §2.9 (Inclusive split); Exclusive merge — это §2.3/§2.7
  (несинхронизирующий pass-through), Inclusive OR-join — §2.10 (SRD-022).
- [ADR-001 v.5](../design/ADR-001-execution-model.ru.md) — механика разветвления
  (`Exec → checkFlows → spawn`), которую кормят split'ы.
- [ADR-010 v.2](../design/ADR-010-process-data-model.ru.md) — данные на
  выполнение, которые читают условия (источник данных `re`).
- Ссылки вверх/вбок, с version-pin; нет ссылок вниз (ADR-005 не цитирует
  SRD-021).

## 9. Definition of Done

- FR-1…FR-7 подключены и задействованы тестами §7, включая тесты маршрутизации
  шлюзов на уровне движка.
- `ExclusiveGateway.Exec` — first-true + с учётом направления; `InclusiveGateway`
  существует с `Exec`, разветвляющим подмножество, и без `SynchronizingJoin`;
  `checkCondition` общий в базе.
- `make ci` зелёный (tidy, lint, build, `-race`, diff-coverage ≥95,
  govulncheck); затронутые файлы ≥80% (цель 100%).
- Запускаемый пример маршрутизации smoke-прогон exit 0; существующие примеры
  по-прежнему exit 0.
- §10 заполнена; статус → Принято; добавлен RU-двойник; ADR-005 переведён в
  Accepted v.2 (его концепция теперь реализована, кроме OR-join — ожидает
  SRD-022); связанные документы синхронизированы.

## 10. Сводка реализации

Приземлено на `feat/routing-gateways` (от `master`): три вехи.

### 10.1 Коммиты

| M | Commit | Scope | Tests |
|---|---|---|---|
| doc | `186b6e4` | Черновик SRD-021 | — |
| M1 | `3d95610` | Exclusive split → §2.8: first-true с коротким замыканием, pass-through с учётом направления, явное исключение default; `checkCondition` перенесён в базовый `Gateway` | `TestExclusiveGatewayExec` (first-true overlap, pass-through, default, exception, non-bool, eval-error) |
| M2 | `754c128` | Новый `InclusiveGateway` — расходящийся `Exec` разветвляет истинное подмножество (§2.9); `NodeExecutor`, не `SynchronizingJoin` | `TestInclusiveSplitSubset`, `TestInclusiveConvergingUnsupported`, `TestNewInclusiveGateway`, `TestInclusiveGatewayClone` |
| M3 | `2cec625` | Тесты маршрутизации на уровне движка + `examples/gateway-routing` | `TestExclusiveRoutingEndToEnd`, `TestInclusiveSplitEndToEnd` |

### 10.2 Ключевые файлы

- `pkg/model/gateways/gateway.go` — общий `checkCondition` (на основе ExpressionEngine, с типом bool).
- `pkg/model/gateways/exclusive.go` — `Exec` приведён к §2.8 (first-true, pass-through, исключение default, exception).
- `pkg/model/gateways/inclusive.go` (new) — split `InclusiveGateway` (истинное подмножество); не `SynchronizingJoin`.
- `pkg/thresher/gateway_routing_test.go` (new) — сквозная маршрутизация XOR/OR.
- `examples/gateway-routing/` (new) — XOR-ветвление на основе данных.

### 10.3 Верификация

- `make ci` зелёный: lint, build, `-race`, **diff-coverage 100% из 97 изменённых
  строк (≥95)**, govulncheck. Затронутые функции шлюзов 100%.
- Все 11 примеров smoke-прогон exit 0.
- Условия вычисляются в рантайме через `ExpressionEngine` у `execEnv` + `Find`
  данных — проверено, что изменений wiring не потребовалось.

### 10.4 Дельты относительно черновика

- **Исключение default-потока сделано явным.** `Exec` Exclusive/Inclusive
  пропускает default-поток по идентичности (`of == defaultFlow`), а не полагаясь
  на отсутствие у него условия — выявлено на ревью M1; правило выбора §2.8/§2.9
  не изменилось.
- Рантайм-wiring (`execEnv` → ExpressionEngine/Find) уже существовал, поэтому M3
  был только тесты + пример — без изменений движка.

## История документа

| Версия | Дата | Автор | Изменение |
|---|---|---|---|
| v.1 | 2026-06-19 | Руслан Габитов | Принято. Приземляет **split'ы** маршрутизации на основе данных из ADR-005 v.2: приводит `ExclusiveGateway.Exec` к §2.8 (**first-true с коротким замыканием** взамен collect-all/error-on-overlap; **pass-through с учётом направления**, чинящий сломанный сходящийся merge; default/exception сохранены) и добавляет новый `InclusiveGateway`, чей расходящийся `Exec` разветвляет **истинное подмножество** (§2.9), с `checkCondition`, вынесенным в базовый `Gateway` для обоих. Условные + default-потоки последовательности шлюза задействованы сквозь; условные потоки на уровне задачи (#51) остаются вне scope (задачи по-прежнему разветвляют все исходящие, ADR-005 §2.7) — отдельная работа, переиспользующая helper условий из этого SRD. Inclusive **OR-join** (§2.10) исключён — родственный SRD-022 — поэтому `InclusiveGateway` реализует `NodeExecutor`, но не `SynchronizingJoin`; сходящийся Inclusive-шлюз не поддерживается до тех пор. Обосновано кодом по `pkg/model/gateways` (gateway.go/exclusive.go/parallel.go), `pkg/model/flow` (sequenceflow.go), `internal/instance` (track.go executeNode/checkFlows). Реализует ADR-005 v.2 §2.8/§2.9; ссылается на ADR-001 v.5, ADR-010 v.2. |
