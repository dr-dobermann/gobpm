# SRD-028 — Позиции токенов, владеемые циклом (исходящий срез ADR-017)

| Поле | Значение |
|---|---|
| Статус | Принято |
| Версия | v.1 |
| Дата | 2026-06-26 |
| Владелец | Ruslan Gabitov |
| Реализует | [ADR-017 v.1 Channel-based event processing](../design/ADR-017-channel-based-event-processing.md) §2 Rule 2 (исходящий срез) |

Этот SRD приземляет **исходящий срез** ADR-017: per-instance **loop становится
единственным владельцем вида позиций токенов / join'ов**, который читают достижимость
и join'ы. Track **эмитит** каждое перемещение позиции в цикл, а не оставляет свою позицию
на чтение циклом; цикл ведёт собственные карты `position` и `parked-at-join`, и машинерия
join'а читает эти карты, а не живой `currentStep()`/`state` другого track'а. Это убирает
кросс-горутинные чтения «loop-reads-track-state» **по построению** — остаточный класс гонки
за переходным ложным abort'ом Complex / OR-join, который SRD-027 §3.8 вылечил лишь
заплаткой single-snapshot (которая всё ещё кросс-читала состояние track'а). **Входящий срез**
(channel-park-доставка — ADR-017 Rule 1) — это SRD-027, уже приземлённый в этой ветке.

---

## 1. Контекст и текущее состояние (проверено по коду)

ADR-001 v.5 делает per-instance **loop** единственным писателем lifecycle-состояния,
а SRD-027 сделал его единственным **диспетчером** входящих событий. Один шов всё ещё
нарушает модель единственного писателя в направлении *чтения*: когда reachability/Complex
join перепроверяется, цикл **читает мутабельные позицию и состояние других track'ов
кросс-горутинно**, чтобы вычислить, какие узлы удерживают живые токены.

- **`joinPositions` сканирует живое состояние каждого track'а (ключевое чтение).** `joinPositions`
  (`internal/instance/reachability.go:83-105`) итерирует `inst.tracks` и, по каждому track'у, читает
  `t.inState(TrackMerged, TrackEnded, TrackCanceled, TrackFailed)` и `t.currentStep().node.ID()`
  — позицию и живость track'ов, исполняющихся на **других** горутинах. Это питает и
  occupied-множество узлов для достижимости, и in-transit-guard.
- **`recheckAwaitingJoins` сканирует припаркованные join'ы, читая состояние.**
  (`internal/instance/instance.go:997-1013`) итерирует `inst.tracks`, читает
  `t.inState(TrackAwaitSync)` и `t.currentStep().node` каждого track'а, чтобы найти, какие
  reachability-join'ы сейчас удерживают припаркованный токен.
- **`recheckParked` читает позицию только что припаркованного track'а.**
  (`internal/instance/instance.go:1020-1031`) `node := t.currentStep().node`.
- **`fireOrJoin` читает позицию выжившего (логирование).**
  (`internal/instance/instance.go:1089-1104`) `survivor.currentStep().node.ID()`.

Все четыре чтения защищены `t.m` (`track.go:188`, через `inState`/`currentStep`), так что это не
*data*-гонки — но это **кросс-горутинные чтения мутабельного состояния другого track'а**, что
ровно то, что запрещает ADR-017 Rule 2. Их защита не делает *вид* консистентным: токен,
соскальзывающий с ветки (reachable) на join (arrived-pending) между двумя такими чтениями, —
это и есть то, что порождало ложный abort «activation rule unsatisfiable». SRD-027 §3.8 сузил
это до единственного снимка `joinPositions` (`fixedFlowChecker`, `reachability.go:27-37`), но снимок
*всё ещё* строится кросс-чтением каждого track'а — заплатка убрала *двойное* чтение, а не само
чтение.

**Цикл узнаёт lifecycle, никогда — перемещения позиций.** Восемь `trackEventKind`
(`internal/instance/event.go:67-100`) сообщают fork / ended / awaiting / merged / parked / failed /
waiting / deliver — **никакого перемещения позиции**. Продвижение по single-flow дописывает шаг
в `checkFlows` (`track.go:799-810`) и не эмитит **ничего** (только *дополнительная* форкнутая ветка
эмитит `evFork`, `track.go:831-833`); продвижение по плечу Event-Based-шлюза дописывает в `advanceToArm`
(`track.go:996-1002`) и не эмитит ничего. Поэтому сегодня у цикла нет иного выбора, кроме как читать
`currentStep()` по требованию.

**Живой путь `FlowChecker` уже мёртв.** После SRD-027 `recheckJoin` строит
`fixedFlowChecker` из одного снимка `joinPositions` и передаёт **его** в каждый `j.Recheck(...)`
(`instance.go:1046-1082`). Единственные места вызова `FlowChecker.CheckFlows`
(`pkg/model/gateways/inclusive.go:146`, `pkg/model/gateways/complex.go:319`) получают этот фиксированный
checker. Ничто больше не конструирует **живой** путь `inst.CheckFlows` (`reachability.go:15-20`) →
`occupiedNodes` (`reachability.go:69-73`); ассерт `exec.FlowChecker = (*Instance)(nil)`
(`reachability.go:143`) — единственное, что держит его компилируемым. Это мёртвая поверхность,
которую этот срез убирает (ср. house-rule об аудите устаревших интерфейсов).

**Состояние track'а уже почти локально.** После SRD-027 `ProcessEvent` только `emit`'ит (он больше
не вызывает `updateState`/`record` — `track.go:898-905`, `instance.go:375-389`), так что концерн
«синхронный waiter пишет `t.steps` из своей собственной горутины», отмеченный у `record`
(`track.go:196`) и `checkFlows` (`track.go:805`), **больше неприменим**. После того как этот срез
уберёт четыре чтения цикла выше, единственным оставшимся кросс-горутинным касанием
`t.steps`/`t.state` будет финализация циклом **затихшего слитого track'а**
(`applyMerged`/`recheckParked` пишут `TrackMerged`, затем `record` читает `t.steps`, после того как
track вернулся или припарковался на `parkCh` — channel happens-before). `t.m` **сохраняется**, чтобы
защитить эту передачу (§3.6).

ADR-017 (Draft) решил структурный фикс. Этот SRD реализует его **исходящую** половину.

## 2. Требования

### Функциональные

- **FR-1 — Цикл владеет видом позиций токенов.** `loop()` ведёт loop-локальную
  `position map[trackID]flow.Node` (текущий узел каждого **живого** track'а) и loop-локальную
  `parked map[trackID]flow.Node` (join-узел каждого track'а, припаркованного на reachability/Complex
  join'е, `TrackAwaitSync`). Обе только-для-loop-горутины — без блокировки, как `waiting`/`msgIdx` (SRD-027).
  Цикл **никогда не читает `currentStep()`/`inState()` другого track'а**, чтобы их построить.
- **FR-2 — Track эмитит свои перемещения позиции.** Всякий раз, когда track продвигается на новый узел,
  он эмитит `evMoved` `trackEvent`, **несущий этот узел** (track его знает — он только что дописал шаг
  на своей собственной горутине). Два места перемещения — это `checkFlows` (обычное продвижение,
  `track.go:799-810`) и `advanceToArm` (продвижение по плечу Event-Based-шлюза, `track.go:996-1002`).
  Цикл выставляет `position[track] = node` по `evMoved`. **Начальная** позиция засевается циклом на
  `spawn` до старта горутины track'а (последовательное чтение во время конструирования, не конкурентное —
  §3.3).
- **FR-3 — Цикл владеет видом parked-at-join из `evParked`.** По `evParked` цикл записывает
  `parked[track] = ev.node` — join-узел, **несомый в событии** — **под guard'ом**, чтобы он записывал
  только живой, незавершающийся track (краевые случаи shutdown'а и гонки слияния — ADR-017 v.1
  §«Rule 2 mechanics»). Track покидает `parked`, когда возобновляется и перемещается (`evMoved` очищает
  его), сливается прочь (`evMerged`) или заканчивается. `recheckAwaitingJoins` итерирует `parked`, а не
  `inst.tracks`.
- **FR-4 — `joinPositions` — чистая функция над картами, владеемыми циклом.** Она выводит
  occupied-множество узлов и in-transit-флаг только из `position`/`parked` — без сканирования
  `inst.tracks`, без `currentStep()`, без `inState()`. Принадлежность и тайминг **идентичны** сегодняшнему
  снимку (§4): occupied = узел каждого живого track'а; in-transit = track, чей узел — это join, но
  которого нет в `parked`.
- **FR-5 — `recheckParked` и `fireOrJoin` читают позицию, владеемую циклом.** Join-узел припаркованного
  track'а берётся из `parked[track]` (или `position[track]`); узел выжившего для логирования берётся из
  `position[survivor]`. Ни один не вызывает `currentStep()`.
- **FR-6 — Живость приходит из lifecycle-событий, а не из чтений состояния.** Track удаляется из
  `position` (и `parked`), когда умирает: `evEnded` / `evFailed` (сам track) и поглощённые id по
  `evMerged`. `evAwaiting` (Parallel-join `TrackAwaitingMerge`) держит track в `position` (его токен
  всё ещё Alive на join'е), но **не** добавляет его в `parked` (это только `TrackAwaitSync`). Это в
  точности воспроизводит сегодняшний dead-фильтр `joinPositions` (`reachability.go:89`).
- **FR-7 — Мёртвый живой `FlowChecker` удалён.** Удалить `inst.CheckFlows`
  (`reachability.go:15-20`), `occupiedNodes` (`reachability.go:69-73`) и ассерт
  `exec.FlowChecker = (*Instance)(nil)` (`reachability.go:143`). `fixedFlowChecker`
  (построенный из occupied-снимка, владеемого циклом) остаётся **единственным** `FlowChecker`;
  `checkFlowsWith` / `reachesOccupied` не изменены.

### Нефункциональные

- **NFR-1 — Никаких чтений позиции/состояния ЖИВОГО track'а со стороны цикла.** После этого среза ни
  одна функция цикла не читает `currentStep()`/`inState()` **исполняющегося** track'а; достижимость и
  join'ы читают карты `position`/`parked`, владеемые циклом. Цикл всё ещё финализирует **затихший**
  слитый track через `record()` (передача единственного писателя ADR-001, §3.6) — это устоявшийся
  паттерн, а не нарушение Rule 2. Проверено аудитом (grep), зафиксированным в §10, и через `-race`.
- **NFR-2 — Семантика достижимости / join'ов байт-в-байт неизменна.** Death-trigger OR-join'а
  (SRD-022) и fire/abort Complex'а (SRD-023) решают идентично: вид occupied/in-transit, владеемый
  циклом, имеет ту же принадлежность и тот же наблюдаемый тайминг, что и сегодняшний снимок
  `joinPositions` (§4 доказывает эквивалентность). Все существующие тесты шлюзов проходят без
  модификации, кроме тех, что напрямую вызывают удалённые/перемещённые внутренности.
- **NFR-3 — `t.m` сохранён; шов merge-пути остаётся под guard'ом.** `t.steps`/`t.state` **не**
  чисто-локальны для track-горутины — цикл финализирует затихший слитый track через `record()` (§3.6) —
  так что per-track-блокировка `t.m` **сохраняется** (теперь неоспариваемая, ведь цикл покинул горячий
  путь достижимости в §3.1–§3.4). Её удаление — намеренный non-goal: оно поставило бы корректность на
  happens-before `emit`/`parkCh` без структурного принуждения. Набор `-race` остаётся чистым (T-6).
- **NFR-4 — Diff-покрытие ≥ COVER_MIN (95%) на затронутых функциях**, цель — 100%.

## 3. Модели

### 3.1 `trackEvent` — kind `evMoved` и поле `node` (`internal/instance/event.go`)

Добавить один kind и одно поле:

```go
type trackEvent struct {
	track     *track
	node      flow.Node            // evMoved: node advanced onto; evParked: the join node
	eDef      flow.EventDefinition
	flows     []*flow.SequenceFlow
	mergedIDs []string
	msgDefIDs []string
	kind      trackEventKind
}

const (
	evFork trackEventKind = iota
	// …existing kinds…
	evDeliver
	// evMoved: the track advanced onto a new node (node carries it). The loop updates its
	// own position view; it never reads the track's currentStep to learn the move.
	evMoved
)
```

`node` несётся **в событии** именно для того, чтобы циклу не нужно было читать
`ev.track.currentStep()` — это чтение и есть убираемое нарушение. Цикл держит `flow.Node` (а не только
его id), потому что машинерия join'а делает type-switch по нему (`node.(exec.ReachabilityJoin)` /
`exec.ActivationJoin`) и читает `node.ID()`. Порядок полей держит `govet`/`fieldalignment`
довольным (поля-интерфейсы/указатели первыми).

### 3.2 Track эмитит на каждом перемещении (`internal/instance/track.go`)

`checkFlows`, сразу после того как дописал следующий шаг под `t.m` (`track.go:808-810`):

```go
t.m.Lock()
t.steps = append(t.steps, &nextStep)
t.m.Unlock()

// Report the advance to the loop, the sole owner of the position view (ADR-017 Rule 2).
t.instance.emit(trackEvent{kind: evMoved, track: t, node: nextStep.node})
```

`advanceToArm` эмитит то же после своего append'а (узел победившего плеча). Оба исполняются только пока
instance Active (`checkFlows`/`advanceToArm` достижимы только из `run`/`deliver`), так что — в отличие от
`evWaiting` — гейтинг времени конструирования не нужен; arm `<-loopDone` у `emit` всё ещё ограничивает
отправку. Track **не** эмитит для своего *начального* узла (у него нет предыдущей позиции, которую можно
покинуть); цикл засевает её на `spawn` (§3.3).

### 3.3 `loop()` — карты position и parked (`internal/instance/instance.go`)

Две loop-локали рядом с `waiting`/`msgIdx`, владеемые loop-горутиной (без блокировки):

```go
position := map[string]flow.Node{} // live trackID → current node
parked   := map[string]flow.Node{} // trackID → join node, for AwaitSync-parked tracks
```

`spawn` засевает начальную позицию до старта run-горутины:

```go
spawn := func(t *track) {
	inst.tracks[t.ID()] = t
	inst.addToSnap(t)
	active++

	// Seed the initial position on the loop goroutine, BEFORE the run goroutine starts
	// (the `go` below). This read is sequential — the track has no other goroutine yet —
	// so it is not a Rule-2 cross-read; every later move arrives as evMoved.
	position[t.ID()] = t.currentStep().node

	if t.inState(TrackWaitForEvent) { … } // unchanged (SRD-027)
	go func(t *track) { … }(t)
}
```

`applyEvent` протягивает `position`/`parked` как `waiting`/`msgIdx` и обновляет их:

| событие | position | parked |
|---|---|---|
| `evMoved` | `position[t] = ev.node` | `delete(parked, t)` (перемещается ⟹ не припаркован) |
| `evParked` | — | `parked[t] = ev.node` — **тогда и только тогда**, когда не останавливается **и** `t` всё ещё в `position` (ADR-017 v.1 §«Rule 2 mechanics») |
| `evAwaiting` | держать (жив на join'е) | — (AwaitingMerge ≠ AwaitSync) |
| `evMerged` | `delete` поглощённых id | `delete` поглощённых id |
| `evEnded` / `evFailed` | `delete(position, t)` | `delete(parked, t)` |

`evParked` несёт join-узел в событии (`ev.node`), так что записанная парковка никогда не зависит от
тайминга `position`; два guard'а на ней (shutdown и гонка слияния, где `evMerged` завершающего прихода
очищает `t` до того, как применён его собственный `evParked`) детально описаны в ADR-017 v.1
§«Rule 2 mechanics».

`stopAll` очищает обе (как `waiting`/`msgIdx`). Хелперы перепроверки (`recheckAwaitingJoins`,
`recheckParked`, `recheckJoin`, `fireOrJoin`) принимают `position`/`parked` параметрами — тот же
паттерн протягивания loop-локалей, который `applyEvent`/`dispatchToParked` уже используют для
`waiting`/`msgIdx`.

### 3.4 `joinPositions` — чистая над картами (`internal/instance/reachability.go`)

```go
// joinPositions derives the occupied-node set and the imminent-arrival flag for a join recheck
// from the loop-owned position/parked maps — no track is read. occupied = every live track's node;
// inTransit = a live token already on joinNode but not yet parked there (between its evMoved onto
// the join and its evParked). Identical membership/timing to the old cross-read snapshot.
func joinPositions(
	joinNode flow.Node,
	position, parked map[string]flow.Node,
) (occupied map[string]bool, inTransit bool) {
	occupied = make(map[string]bool, len(position))

	for id, n := range position {
		occupied[n.ID()] = true

		if joinNode != nil && n.ID() == joinNode.ID() {
			if _, isParked := parked[id]; !isParked {
				inTransit = true
			}
		}
	}

	return occupied, inTransit
}
```

`recheckJoin` вызывает `joinPositions(node, position, parked)` и строит `fixedFlowChecker{occupied}`
как сегодня (`instance.go:1046-1051`); in-transit-defer не изменён.

### 3.5 Удалить мёртвый живой `FlowChecker` (`internal/instance/reachability.go`)

Удалить `inst.CheckFlows` и `occupiedNodes` (нет вызывающих после SRD-027 — §1) и ассерт
`exec.FlowChecker = (*Instance)(nil)`. `fixedFlowChecker`, `checkFlowsWith` и `reachesOccupied`
остаются. Интерфейс `exec.FlowChecker` (`pkg/exec/exec.go:40`) не изменён — `fixedFlowChecker`
по-прежнему его реализует.

### 3.6 `t.m` сохранён — merge-путь финализирует затихший track (`internal/instance/track.go`)

После §3.1–§3.5 цикл больше не читает `steps`/`state` **живого** track'а: машинерия достижимости и
join'ов читает карты `position`/`parked`, владеемые циклом (§3.4). Это удовлетворяет фактическое
требование ADR-017 Rule 2 — *никаких кросс-горутинных чтений позиции/состояния исполняющегося track'а*.

Однако `t.steps`/`t.state` **не** чисто-локальны для track-горутины. Цикл всё ещё финализирует
**затихший слитый** track: `applyMerged` / `recheckParked` вызывают `updateState(TrackMerged)` →
`record()`, который читает `steps[last]` на **loop**-горутине. Собственная горутина этого track'а уже
вернулась (`AwaitingMerge`) или приостановлена на `parkCh` (`AwaitSync`), так что чтение упорядочено
после последней записи track'а передачей `emit` / `parkCh` — паттерн единственного писателя ADR-001,
применённый к затихшему track'у, а не конкурентное чтение.

Из-за этого доступа merge-пути per-track-блокировка `t.m` **сохраняется**, а не удаляется:

- Она теперь **неоспариваема** (цикл вынес её из горячего пути достижимости в §3.1–§3.4) и
  единообразно защищает один оставшийся шов через обе горутины, так что её сохранение почти бесплатно.
- `track` — это **неэкспортируемая сущность пакета `instance`** — цикл и track суть две горутины,
  кооперирующиеся внутри одной внутренней абстракции, а не публичная граница, обязанная обещать
  изоляцию горутин. Передача затихшего track'а под guard'ом между ними — легитимный
  внутрипакетный дизайн, а не утёкший инвариант.
- Полное удаление блокировки поставило бы корректность на рассуждение о happens-before `emit`/`parkCh`
  *без* структурного принуждения — намеренный **non-goal** (это меняет бесплатный, безопасный guard на
  подверженность тонкой гонке при любом будущем изменении merge/resume-путей).

Устаревшие doc-комментарии у `record` и `deliver` — которые обосновывали guard удалённым
`occupiedNodes` и `path()` (который на самом деле читает lock-free `hist`, никогда `t.steps`) —
исправлены, чтобы назвать настоящего читателя merge-пути.

## 4. Анализ

**Почему emit-on-move (выбрано).** Достижимости нужен текущий узел **каждого живого токена**, включая
in-flight (не-припаркованные) — активно перемещающийся вышестоящий токен — это потенциальный приход,
которого join обязан ждать. Большинство перемещений сегодня не эмитят события (§1), так что, чтобы
цикл владел occupied-видом, он обязан узнавать каждое перемещение; ADR-017 §3 уже это принимает
(«обычный пост-доставочный `emit` track'а, сообщающий о его продвижении обратно в цикл … этот
исходящий notify неизбежен в любом дизайне»). Сбор токенов имеет ту же нужду — живые токены instance'а
*и есть* per-track-позиции — так что вид, владеемый циклом, — естественный дом для обоих (forward-синергия;
этот срез не перенацеливает `GetTokens`, который продолжает читать lock-free проекцию `hist` — §5).

**Эквивалентность старому снимку (NFR-2).** Сегодня `joinPositions` включает track тогда и только
тогда, когда он не в `{Merged, Ended, Canceled, Failed}`, и читает его `currentStep()`. Карта
`position`, владеемая циклом, держит track от его засева на `spawn` до тех пор, пока
`evEnded`/`evFailed`/`evMerged-absorbed` не удалит его — т.е. ровно not-dead-множество (Canceled
случается только под `stopAll`, который очищает карты). Каждое перемещение обновляет узел через
`evMoved`, а `inst.events` — FIFO, так что вид цикла на узел track'а — это его реальный узел на момент
последнего перемещения, которое он осушил, — то же значение, что вернул бы `currentStep()` в момент,
когда цикл обрабатывает триггерящее lifecycle-событие. Окно in-transit (узел = join, ещё не `parked`) —
это интервал между `evMoved` track'а на join и его `evParked`, который цикл видит именно в этом
порядке, — то же окно, что детектировал `!inState(TrackAwaitSync)`. Принадлежность и тайминг,
следовательно, совпадают.

**Рассмотренные альтернативы.**

- **A — Emit-on-move, карты `position`/`parked`, владеемые циклом (выбрано).** Каждое продвижение
  эмитит `evMoved`; цикл выводит occupied/in-transit из собственных карт. Убирает все четыре
  кросс-чтения и мёртвый живой `FlowChecker`. Цена: один `emit` на шаг по узлу (дёшево — цикл лишь
  обновляет карту; исполнение узла остаётся на track-горутине).
- **B — Нести позицию только в существующих lifecycle-событиях.** Отклонено: большинство продвижений
  не эмитят lifecycle-события, так что цикл пропускал бы in-flight-токены между join'ами — ровно те
  токены, которые достижимость обязана видеть. Он не может реконструировать occupied-множество только
  из lifecycle-событий.
- **C — Поддерживать позиции только когда граф процесса имеет reachability/Complex join.** Реальная
  оптимизация (parallel-only или join-free процесс никогда не консультируется с занятостью), но
  спекулятивная — она обусловливает горячий путь свойством графа ради экономии записи в карту. Отложена
  за forward-заметкой; добавлять только при измеренной стоимости (ср. house-rule о
  no-speculative-universality).

## 5. Поверхность публичного API

**Нет.** `position`/`parked` — loop-локали; поле `node` у `evMoved`/`evParked` — пакетно-внутреннее.
`GetTokens` / `TokenHistory` (`instance.go:1126-1158`) **не изменены** — они выводятся из lock-free
атомарной проекции `hist`, что и делает их безопасными для вызова из горутины внешнего наблюдателя
(SRD-018). Они намеренно **не** перенацелены на карту `position`, владеемую циклом, по двум причинам:

1. **Безопасность.** `position` — только-для-loop-горутины (без блокировки), так что внешнее чтение было
   бы data-гонкой. Её экспонирование потребовало бы, чтобы цикл публиковал атомарный снимок позиций —
   избыточно с `hist`.
2. **Они легитимно отличаются.** Последняя запись `hist` пишется, когда узел *начинает исполняться*
   (`record(TrackExecutingStep)` в `prepareNodeExecution`), так что она отстаёт от `position`
   (выставленной на `checkFlows`/`evMoved`) до одного шага во время перемещения. Достижимости нужна
   **более актуальная** `position` — in-flight-токен должен считаться на своём новом узле — тогда как
   наблюдение корректно обслуживается записанным `hist` (внешний читатель видит валидный,
   eventually-consistent снимок).

Унификация этих двух (цикл публикует атомарный вид позиций, который читает `GetTokens`) — возможное
будущее изменение, но **вне области** здесь и сомнительной пользы.

## 6. Тестовые сценарии

- **T-1 — `evMoved` обновляет позицию цикла.** Прогнать track через два узла; проверить, что
  `position[track]` цикла следует за каждым `evMoved` и что цикл не читает `currentStep()` (хелпер
  наблюдает только карты).
- **T-2 — `joinPositions` чиста над картами.** Табличный тест на свободной функции: даны карты
  `position`/`parked` (без `*Instance`, без track'ов), проверить `occupied` и `inTransit` для: токен
  на join'е + не припаркован → in-transit; токен на join'е + припаркован → не in-transit; токен в
  другом месте → occupied, не in-transit; пустые карты → empty/false. Заменяет
  `TestJoinPositionsInTransit` (`reachability_loop_test.go:11-30`), который строил состояние мутацией
  реальных track'ов.
- **T-3 — OR-join срабатывает идентично (регрессия).** «Ромб» SRD-022: проверить, что тайминг
  срабатывания и исход survivor/merged неизменны с видом, владеемым циклом.
- **T-4 — `recheckAwaitingJoins` итерирует `parked`.** С двумя track'ами, припаркованными на одном
  OR-join'е и записанными в `parked`, гибель токена триггерит ровно один `recheckJoin` для этого узла —
  без сканирования `inst.tracks`, без чтения `inState`.
- **T-5 — Fire/abort Complex'а неизменны (регрессия).** Сценарии SRD-023
  (`TestComplexRequiredGate`, `TestComplexAbortOnDeath`, `TestComplexAbortInstance`): срабатывание при
  удовлетворённом правиле, abort + детерминированное завершение при неудовлетворимом, под новым видом.
- **T-6 — `-race`-стресс, без кросс-чтений.** `pkg/thresher` под `-race` ×40 остаётся зелёным (базовая
  линия SRD-027), подтверждая, что удалённые чтения не внесли регрессии и сокращение `t.m` чисто.
- **T-7 — Мёртвый `FlowChecker` удалён.** Время компиляции: `inst.CheckFlows`/`occupiedNodes`/ассерт
  `(*Instance)` ушли; `fixedFlowChecker` остаётся единственным `exec.FlowChecker`.
- **T-8 — Shutdown-guard `evParked`.** `applyEvent(evParked)` с `stopping = true` ничего не записывает
  (`parked` остаётся пустым) и не паникует (ADR-017 v.1 §«Rule 2 mechanics», shutdown).
- **T-9 — Merge-race-guard `evParked`.** `applyEvent(evParked)` для track'а, отсутствующего в `position`
  (уже слитого завершающим приходом), отбрасывает парковку — `parked` остаётся пустым (ADR-017 v.1
  §«Rule 2 mechanics», гонка слияния). Найден `-race`-стрессом T-6 (`TestORJoinAllBranchesArrive`
  nil-паниковал до guard'а).

## 8. Cross-doc

- **Реализует** [ADR-017 v.1](../design/ADR-017-channel-based-event-processing.md) §2 Rule 2
  (исходящий срез), §3 (гонка устранена по построению), §7 (срез 2 из 2).
- **Машинерия достижимости** — [ADR-005 v.4](../design/ADR-005-gateways-and-joins.md) §2.10
  (OR-join) / §2.11 (Complex `ActivationJoin`); этот срез меняет только *откуда берётся occupied-множество*,
  а не контракт `FlowChecker` (`pkg/exec/exec.go`).
- **Связан с** SRD-022 (death-trigger OR-join'а), SRD-023 (Complex-шлюз) и SRD-027 (входящий срез) —
  этот срез **замещает заплатку single-snapshot из SRD-027 §3.8**, убирая кросс-чтение целиком.
  (Ссылки на SRD не несут version-пина — SRD/FIX одноразовы.)
- Иерархия: SRD → ADR | SAD | SRD только (вверх/вбок); version-пины только на ссылках ADR/SAD.

## 9. Definition of Done

- [x] FR-1…FR-7 подключены; `position`/`parked` владеемы циклом; `evMoved` эмитится в обоих местах
      перемещения; живой `FlowChecker` удалён.
- [x] NFR-1 аудит: не осталось ни одного чтения `currentStep()`/`inState()`/`t.steps`/`t.state` другого
      track'а со стороны цикла (grep зафиксирован в §10).
- [x] §6 тесты добавлены и проходят; существующие наборы шлюзов/instance зелёные (NFR-2).
- [x] `make ci` зелёный по всем модулям; diff-покрытие ≥ 95% на затронутых функциях (NFR-4), цель
      100% (99.7%, §10 V-5).
- [x] Примеры собираются **и запускаются** (дисциплина runtime-smoke из SRD-027).
- [x] §10 заполнен (файлы/строки, V-результаты, SHA майлстоунов); flip статуса — решение владельца.

## 10. Сводка по реализации

Приземлено на `feat/adr-017-eps-rework` в два майлстоуна.

**Затронутые файлы**

| Файл | Изменение |
|------|--------|
| `internal/instance/event.go` | kind `evMoved` + arm `String()`; поле `node flow.Node` на `trackEvent` (несёт узел перемещения для `evMoved`, join-узел для `evParked`). |
| `internal/instance/track.go` | `checkFlows`/`advanceToArm` эмитят `evMoved` после append'а шага; `synchronize`/`synchronizeActivation` эмитят `evParked`, несущий join-узел; исправлены doc-комментарии `record()`/`deliver()` (финализация затихшего слитого track'а). |
| `internal/instance/instance.go` | карты `position`/`parked`, владеемые циклом; `spawn` засевает начальную позицию; `applyEvent` протягивает + обновляет обе (вкл. shutdown- и merge-race-guard'ы `evParked`); хелперы `clearPosition`/`nodeIDOf`; `applyMerged`/`recheckAwaitingJoins`/`recheckParked`/`recheckJoin`/`fireOrJoin` читают карты. |
| `internal/instance/reachability.go` | `joinPositions` переписан как чистая свободная функция над картами; мёртвый живой `FlowChecker` (`inst.CheckFlows`/`occupiedNodes`/ассерт `(*Instance)`) удалён; `fixedFlowChecker`/`checkFlowsWith`/`reachesOccupied` сохранены. |
| `internal/instance/reachability_loop_test.go`, `reachability_test.go` | T-1…T-9 (чистая таблица `joinPositions`, `evMoved`, итерация parked, trailing-park, shutdown- + merge-race-guard'ы, `nodeIDOf`, `checkFlowsWith`). |

**Коммиты майлстоунов**

| Майлстоун | SHA | Scope |
|-----------|-----|-------|
| M1 — позиции, владеемые циклом (emit-on-move) | `333b419` | `evMoved`, две карты, чистый `joinPositions`, мёртвый `FlowChecker` удалён, T-1…T-7. |
| M2 — guard'ы `evParked` (гонка слияния + shutdown) | `b361236` | join-узел несётся в `evParked`; два guard'а; T-8/T-9. `-race`-стресс (`TestORJoinAllBranchesArrive`) вскрыл merge-race nil до guard'а. |
| M2 — выравнивание документации | `98a7b76` | ADR-017 Rule 2 + подсекция `Rule 2 mechanics`; SRD-028 (этот документ) — к реальности единственного писателя; `t.m` сохранён (Option A). |

**Результаты верификации**

- **NFR-1 grep-аудит.** Не осталось ни одного чтения `currentStep()`/`inState()` **живого** track'а
  loop-горутиной. Остаточные обращения — это (a) seed-засев на spawn во время конструирования
  (`instance.go:689/696`, последовательный, до `go func` track'а) и (b) `trackEndKind`
  (`instance.go:969-979`), вызываемый на `:711` **внутри собственной горутины track'а после возврата
  `run()`** — затихшее само-чтение, а не чтение живого track'а циклом.
- **V-1 (T-1…T-9).** `go test -race -run 'TestJoinPositions|TestApplyEvent|TestRecheck|TestNodeIDOf|TestCheckFlowsWith' ./internal/instance/` — зелёный.
- **V-2 (регрессия).** Наборы шлюзов OR-join (SRD-022) + Complex (SRD-023) зелёные под `make ci -race`.
- **V-3 (`-race`-стресс, T-6).** `pkg/thresher` под `-race` ×40 — `THR_EXIT=0`, без data-гонки.
- **V-4 (`make ci`).** Зелёный по всем модулям (tidy, lint 0 issues, build, `-race`-тесты,
  diff-покрытие, govulncheck).
- **V-5 (diff-покрытие, NFR-4).** `covercheck -min 95 -base origin/master`: **99.7%** из 346 изменённых
  покрываемых строк — PASS. instance.go / event.go / reachability.go 100%, track.go 98.7% (76/77).
- **V-6 (примеры build + run).** Все 16 модулей `examples/*` запускаются до exit 0 (`go run .` на
  модуль, ≤40 с каждый) — CI их только собирает; runtime-smoke этого среза прогнал полный набор, включая
  релевантные шлюзам `inclusive-join` / `complex-gateway` / `parallel-gateway` / `gateway-routing` /
  `event-based-gateway`.

## Открытые вопросы

Нет.
