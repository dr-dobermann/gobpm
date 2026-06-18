# FIX-004 «Параллельные экземпляры на одном timer-catch делят один waiter — таймер срабатывает у всех»

> Перевод. Канонична английская версия: [FIX-004-timer-catch-broadcast.md](FIX-004-timer-catch-broadcast.md). При расхождении приоритет у английского текста.

**Тип:** FIX (одноразовый багфикс; после приземления не переписывается).
**Статус:** Приземлён v.1 (2026-06-18, ветка `fix/timer-catch-broadcast`, реализован).
**Дата:** 2026-06-18.
**Автор:** Руслан Габитов.
**Ветка:** `fix/timer-catch-broadcast` (один точечный дефект — определение timer-события получает идентичность per-instance, ровно как message-определение в SRD-017).
**Парный документ:** нет (точечная правка; механизм per-instance-идентичности eDef — прецедент SRD-017, см. §7).
**Вышестоящее:** [SRD-017 v.1](../srd/SRD-017-conversation-token-threading.md) (message-прецедент — `CloneForInstance`, §4.3); [ADR-006 v.1](../design/ADR-006-events-and-subscriptions.md) (модель waiter'ов EventHub); [ADR-009 v.1](../design/ADR-009-per-instance-node-graph.md) (каждый экземпляр владеет приватной копией графа узлов).

**Обосновано (внутренние артефакты):**
- SRD-017 исправил это **только для message-определений событий** (`MessageEventDefinition.CloneForInstance`, M3a) и явно отложил не-message типы в «собственный FIX» (SRD-017 §4.3, финальная заметка). Это и есть тот FIX.
- Каждое утверждение ниже перепроверено по `master` на `221ea3f` (после мержа SRD-017) — номера строк актуальны.

## §1 Симптомы

### §1.1 Симптом: одно срабатывание таймера возобновляет каждый параллельный экземпляр, ожидающий на том же timer-catch

Два (или больше) запущенных экземпляра одного процесса, каждый припаркован на
одном и том же промежуточном **timer**-catch-событии, делят **один** waiter
EventHub. Когда таймер срабатывает, waiter возобновляет их **все** разом —
вместо того чтобы таймер каждого экземпляра возобновлял только этот экземпляр.
Это неверно по BPMN (§10.5: catch-событие возобновляет токен в *своём*
экземпляре процесса) и недетерминированно с точки зрения оператора: N
экземпляров завершаются по одному таймеру.

Это точный аналог message-broadcast-бага, который исправил SRD-017, но для
таймеров — и он сейчас **замаскирован**, потому что ни один тест не запускает
два параллельных экземпляра, припаркованных на одном timer-catch.

```
instance A ─┐
            ├─ both parked at intermediate timer catch "wait-15m" (same eDef id)
instance B ─┘
   timer fires once  ─▶  ONE shared waiter ─▶ fireDefinition → resumes A AND B
```

В коде: промежуточный timer-catch регистрируется через
`internal/instance/track.go:310` (`t.instance.RegisterEvent(t, d)`); EventHub
ключует waiter'ы по `eDef.ID()` и **сливает** повторную регистрацию того же id
на существующий waiter через `AddEventProcessor`
(`internal/eventproc/eventhub/eventhub.go:173`). Поскольку timer-eDef обоих
экземпляров несут **одинаковый** id (см. §2), они попадают на один waiter; при
срабатывании он проходит по всем зарегистрированным процессорам — обоим трекам.

## §2 Анализ первопричины

### §2.1 `Event.clone()` делит не-message определения событий по ссылке

Каждый экземпляр владеет приватной копией графа узлов (ADR-009). Клонирование
узлов идёт через `Event.clone()` (`pkg/model/events/event.go:161`), чей
`cloneDefsForInstance` (`event.go:175`) выдаёт **свежий per-instance id только
определениям, реализующим опциональный интерфейс `CloneForInstance`**:

```go
func cloneDefsForInstance(defs []flow.EventDefinition) []flow.EventDefinition {
	out := make([]flow.EventDefinition, len(defs))
	for i, d := range defs {
		if c, ok := d.(interface {
			CloneForInstance() flow.EventDefinition
		}); ok {
			out[i] = c.CloneForInstance()
			continue
		}
		out[i] = d            // <-- shared by reference: same id across instances
	}
	return out
}
```

Только `MessageEventDefinition` реализует `CloneForInstance`
(`pkg/model/events/message.go:155`, добавлено в SRD-017 M3a).
`TimerEventDefinition` (`pkg/model/events/timer.go:11`) — **нет**, поэтому копия
каждого экземпляра сохраняет **шаблонный** timer-eDef (ветка `out[i] = d`), т.е.
тот же `eDef.ID()`. EventHub затем сливает их на один waiter (§1.1).

### §2.2 Таймер — единственный не-message тип, который сегодня реально может коллизировать

IntermediateCatchEvent допускает триггеры conditional / signal / timer
(`pkg/model/events/intermediate_catch.go:18` `intermediateCatchTriggers`), но
фабрика waiter'ов строит waiter **только** для timer и message:

```go
// internal/eventproc/eventhub/waiters/waiters.go (CreateWaiter)
case flow.TriggerTimer:   w, err = NewTimeWaiter(eh, ep, eDef, "", rt)
case flow.TriggerMessage: w, err = NewMessageWaiter(eh, ep, eDef, "", rt, true)
default:                  err = ... "couldn't find builder for ... %s"
```

Так что **signal**- или **conditional**-catch можно *смоделировать*, но он
**не зарегистрирует waiter** (ветка `default`-ошибки) — он не может ждать
in-instance, значит не может попасть в этот баг. Message уже исправлен
(SRD-017). **Таймер, следовательно, — единственное catchable, обеспеченное
waiter'ом, не-message определение события, демонстрирующее broadcast** — что и
делает этот FIX timer-scoped.

### §2.3 Почему ни один тест не поймал это

`grep` timer-тестов (`internal/eventproc/eventhub/eventhub_timer_test.go`,
`pkg/model/events/*timer*_test.go`) показывает: ни один не регистрирует **два**
процессора на одном timer-eDef и не утверждает, что сработал только один;
канарейка per-instance-идентичности
(`pkg/model/events/clone_for_instance_test.go`) покрывает **только** message
(`TestMessageReceiverPerInstanceClone`). Пробел — отсутствие утверждения
timer-distinct-id / no-broadcast.

## §3 Решение

### §3.1 Рассмотренные альтернативы

| Альтернатива | За | Против | Решение |
|---|---|---|---|
| A. Добавить `CloneForInstance` в **`TimerEventDefinition`** (структурно, по образцу `message.go:155`) | Один метод; без правки `Event.clone` (его проверка опционального интерфейса уже применяет его); без правки интерфейса `flow.EventDefinition`; без regen мок; точно по образцу SRD-017 | Per-type (повторять при появлении waiter'а у другого catchable-типа) | ✅ выбрано |
| B. Добавить `CloneForInstance` в **интерфейс `flow.EventDefinition`** (обязать каждую реализацию) | Compile-time-гарантия, что каждый eDef per-instance | Затрагивает ~10 реализаций eDef + интерфейс + regen `mockflow`, ради типов, которые **не могут** ждать сегодня (нет builder'а waiter'а) — churn без текущей выгоды (YAGNI) | ❌ отклонено (вернуться, когда catchable станут многие типы) |
| C. Ключевать waiter'ы EventHub по `(eDefID, processorID)` вместо `eDefID` | Чинит все типы событий сразу, без правки модели | Задевает закалённое FIX-003 ядро удаления (`WaiterFired`/`RemoveWaiter`/`UnregisterEvent` все ключуют по `eDefID`); больший радиус поражения ради бага одного типа | ❌ отклонено |

Вариант A — минимальная, согласованная с прецедентом правка для единственного
реально затронутого типа.

### §3.2 Изменения по файлам

#### §3.2.1 `pkg/model/events/timer.go` — `CloneForInstance` со свежим id

```go
// CloneForInstance returns a per-instance copy of the TimerEventDefinition with
// a FRESH id, sharing the (immutable) timer expressions by reference. Node
// cloning (Event.clone) uses it so each process instance's timer catch registers
// a DISTINCT EventHub waiter: without it concurrent instances waiting on the same
// timer would share one waiter and a single timer occurrence would resume them
// all (FIX-004; the timer analog of MessageEventDefinition.CloneForInstance,
// SRD-017 §4.3). A timer carries no payload, so there is no fire-path CloneEvent
// to keep id-stable — only the registration identity must be per-instance.
func (ted *TimerEventDefinition) CloneForInstance() flow.EventDefinition {
	return &TimerEventDefinition{
		definition:   definition{BaseElement: *foundation.MustBaseElement()},
		timeDate:     ted.timeDate,
		timeCycle:    ted.timeCycle,
		timeDuration: ted.timeDuration,
	}
}
```

Добавляет импорт `foundation` в `timer.go`. Других продакшн-изменений нет:
`cloneDefsForInstance` из `Event.clone` уже маршрутизирует любую реализацию
`CloneForInstance` через путь свежего id (§2.1), так что timer-копия становится
per-instance автоматически.

## §4 Верификация

Текущее покрытие в каталоге тестов:
- unit: доставка / жизненный цикл timer-waiter'а есть, но **нет** утверждения
  per-instance-идентичности или two-instance-no-broadcast для таймеров (§2.3).
- message-канарейка (`TestMessageReceiverPerInstanceClone`) — образец для
  повторения.

### §4.1 Регрессионные тесты (обязательны)

#### §4.1.1 `TestTimerReceiverPerInstanceClone`

**Новое:** `pkg/model/events/clone_for_instance_test.go` (рядом с message-тестом).

| Тест | Установка | Утверждение |
|---|---|---|
| `TestTimerReceiverPerInstanceClone` | `IntermediateCatchEvent` с `TimerEventDefinition`; `Clone()` дважды | id двух копий timer-eDef **различаются** между собой и с шаблоном (по образцу message-канарейки) |
| `TestTimerEventDefinitionCloneForInstance` | `CloneForInstance()` на timer-eDef дважды | каждый даёт свежий id; timer-выражения делятся по ссылке |

#### §4.1.2 Two-instance no-broadcast (уровень движка, если выполнимо)

Тест `pkg/thresher` или `internal/instance`: два экземпляра процесса с
промежуточным timer-catch, короткий таймер; утверждать, что каждый экземпляр
возобновляется **независимо** (срабатывание таймера у одного не завершает
другого). (Оценить при реализации; distinct-id-канарейка §4.1.1 — основное
доказательство: различные id ⇒ различные waiter'ы ⇒ нет общего срабатывания,
по ключеванию EventHub.)

## §5 Предотвращение

- **Doc-комментарий** на `TimerEventDefinition.CloneForInstance` называет
  инвариант (per-instance-идентичность регистрации) и канарейку.
- **Заметка на будущее (реальное предотвращение):** требование
  per-instance-идентичности применимо к **каждому catchable-определению
  события, получающему builder waiter'а**. Сегодня builder'ы есть только у
  timer + message (`CreateWaiter`, §2.2). Когда будущее изменение добавит
  case `CreateWaiter` для signal / conditional / и т.п., оно ОБЯЗАНО добавить и
  `CloneForInstance` этому типу eDef — иначе вновь вводит этот broadcast-баг.
  Зафиксировать это рядом со switch'ем `CreateWaiter` комментарием, чтобы
  требование было видно в точке изменения.

## §6 Регрессии / побочные эффекты

### §6.1 Путь срабатывания таймера
Таймер не несёт payload и не имеет `CloneEvent` (он не `flow.EventDefCloner`),
так что срабатывает на **зарегистрированном** (теперь per-instance) eDef
как есть — что по-прежнему совпадает с его собственным waiter'ом.
Существующие тесты доставки timer-waiter'а (`eventhub_timer_test.go`) страхуют,
что таймер по-прежнему срабатывает у своего экземпляра.

### §6.2 Что может полагаться на старое (shared-id) поведение
`grep` любого кода, ищущего таймер по *шаблонному* eDef id между экземплярами →
не ожидается (карта EventHub per-registration; `WaiterFired`/`UnregisterEvent`
используют живой eDef id, который теперь per-instance, ровно как для
message-фикса). Перепрогнать audit-grep перед приземлением.

### §6.3 Путь отката
Откат одним коммитом (метод + его тест); без миграции, без данных.

## §7 Связанное

- [SRD-017 v.1](../srd/SRD-017-conversation-token-threading.md) —
  message-прецедент (`CloneForInstance`, M3a, §4.3) и явный отлог, который
  этот FIX закрывает. Вбок (FIX → SRD).
- [ADR-006 v.1](../design/ADR-006-events-and-subscriptions.md) — модель
  waiter'ов EventHub (ключевание по eDef id; удалением владеет hub).
- [ADR-009 v.1](../design/ADR-009-per-instance-node-graph.md) — каждый
  экземпляр владеет приватной копией графа узлов; per-instance-идентичность
  eDef — естественное завершение этого принципа для catchable-определений.
- **Кандидат на повышение до ADR:** если/когда catchable станут несколько типов
  событий, правило «каждое обеспеченное waiter'ом catch-определение —
  per-instance» стоит сформулировать единожды (ADR или заметка SAD), а не
  per-type.

## §8 Сводка реализации (постадийные фактические приземления + отличия от черновика)

### §8.1 Стадии по коммитам (ветка `fix/timer-catch-broadcast`)

| Стадия | Коммит | Объём | Тесты |
|---|---|---|---|
| doc | `fb683ac` | документ FIX-004 | — |
| M1 | `b98c069` | `TimerEventDefinition.CloneForInstance` (timer.go) + импорт `foundation`; заметка на будущее у switch'а `CreateWaiter` (waiters.go); две канарейки (clone_for_instance_test.go) | `TestTimerEventDefinitionCloneForInstance`, `TestTimerReceiverPerInstanceClone` |

Приземлено ровно по черновику §3.2: один метод, без правки `Event.clone` /
интерфейса / мок. Существующая проверка опционального интерфейса в
`cloneDefsForInstance` применила новый метод автоматически — receiver-канарейка
доказывает это через реальный путь клонирования.

### §8.2 Результаты верификации

- `make ci` (CI-parity-гейт): **PASS** — tidy / golangci-lint (вкл.
  fieldalignment, misspell) / build / `-race`-тесты / diff-coverage /
  govulncheck все зелёные. `TimerEventDefinition.CloneForInstance` измерен на
  **100%** покрытия.
- Все 9 запускаемых примеров выходят с кодом 0, включая timer-примеры
  `simple-timer` и `timer-event` (таймеры по-прежнему срабатывают корректно —
  нет регрессии пути срабатывания, подтверждает §6.1).
- Существующий `TestNonMessageDefSharedOnClone` (signal остаётся shared)
  по-прежнему проходит — на per-instance перешёл только timer-тип, как и
  задумано.

### §8.3 Эмпирические находки — где реальность разошлась с черновиком §3

Нет. Фикс приземлился по проекту; единственные авторские правки —
US-орфография (`analogue`→`analog`) и перенос строк комментария под
80-колоночный / misspell-линтеры проекта — без поведенческой разницы.

### §8.4 Бэклог (вне объёма FIX-004)

- Signal / conditional промежуточные catch'и: когда получат builder
  `CreateWaiter`, каждый ОБЯЗАН также реализовать `CloneForInstance` (см.
  заметку на будущее §5), иначе этот класс broadcast'а вернётся для них.
- Two-instance уровня движка no-broadcast-утверждение (§4.1.2) покрыто
  distinct-id-канарейкой, а не отдельным thresher-тестом — будущий
  интеграционный тест мог бы прогонять его end-to-end, если будет собран
  более широкий набор тестов broadcast'а событий.

## §9 Открытые вопросы

Нет. Объём — **только timer** (единственное не-message catchable, обеспеченное
waiter'ом определение события — §2.2): добавить
`TimerEventDefinition.CloneForInstance` (свежий per-instance id, выражения
делятся), по образцу message-фикса, с distinct-id-канарейкой и заметкой на
будущее, связывающей будущие case'ы `CreateWaiter` с требованием
per-instance-идентичности. Signal/conditional и подход с широким
интерфейс-методом явно вне объёма, пока эти типы не получат builder'ы waiter'ов
(§3.1 B).
