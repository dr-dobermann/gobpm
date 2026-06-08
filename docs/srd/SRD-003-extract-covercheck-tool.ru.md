# SRD-003 — Вынос covercheck в отдельный инструмент

| Поле | Значение |
|---|---|
| Статус | Принято |
| Версия | v.1 |
| Дата | 2026-06-08 |
| Владелец | Руслан Габитов |
| Уточняет | [SAD-001 v.1 §6 Quality Attributes](../design/SAD-001-vision-and-architecture.md) |
| Замещает | [SRD-002 v.1 §4.2](SRD-002-ci-diff-coverage-gate.ru.md) — решение «DIY in-repo» о месте инструмента |

> EN-оригинал — канонический: [SRD-003-extract-covercheck-tool.md](SRD-003-extract-covercheck-tool.md). Этот файл — его перевод (twin).

Этот SRD выносит реализацию гейта diff-покрытия **из** gobpm в собственный репозиторий, потребляемый как закреплённый внешний dev-инструмент. Меняется только **где живёт инструмент**; семантика гейта, область и порог (SRD-002 §2/§3, `COVER_MIN`) не меняются.

## 1. Предпосылки и мотивация

[SRD-002](SRD-002-ci-diff-coverage-gate.ru.md) приземлил гейт как in-repo Go-код (`internal/covercheck` + `cmd/covercheck`), выбранный в §4.2 за «нулевую зависимость / полную локальную парность».

Этот код **доменно-нейтрален** — он парсит Go-coverprofile и вывод `git diff`; в нём нет ничего про BPMN. Держать обобщённый инструмент покрытия внутри модуля движка — запах cohesion: это конфликтует с minimal-and-focused этосом библиотеки (SAD-001 G2) и с границами модулей, которые проводит ADR-003 (модуль движка должен держать код движка). Тот же аргумент парности, который оправдывал in-repo, полностью сохраняется **закреплением версии** внешнего инструмента — ровно так, как репозиторий уже обращается с mockery / golangci-lint / govulncheck.

## 2. Решение

- Инструмент живёт в своём репозитории: **`github.com/dr-dobermann/covercheck`** (релиз **v0.1.1**; пакет `covercheck` покрыт на 100%, тонкий CLI `cmd/covercheck`, свой CI, который dogfood'ит гейт).
- gobpm **потребляет его как закреплённый dev-инструмент**: `make tools` запускает `go install github.com/dr-dobermann/covercheck/cmd/covercheck@$(COVERCHECK_VERSION)`; `make cover-check` вызывает установленный бинарь `covercheck` за guard'ом `require-tool`; `.github/workflows/check.yml` ставит его рядом с прочими закреплёнными инструментами.
- Это идентичная парность in-repo-версии — один и тот же бинарь решает вердикт локально и в CI — минус in-repo-код.

## 3. Функциональные требования

| # | Требование | Приёмка |
|---|---|---|
| FR-1 | `internal/covercheck` и `cmd/covercheck` удалены из gobpm. | Ни один путь не существует; ни один Go-файл их не импортирует; сборка проходит. |
| FR-2 | `make tools` ставит `covercheck` закреплённой версии (`COVERCHECK_VERSION`). | После `make tools` `covercheck` в `PATH` на закреплённом теге. |
| FR-3 | `make cover-check` запускает установленный бинарь `covercheck` за `require-tool` (громко падает при отсутствии). | Отсутствие бинаря прерывает с install-подсказкой; присутствующий гейтит как раньше. |
| FR-4 | CI ставит `covercheck` и запускает тот же `make cover-check`. | Шаг гейта в workflow `check` проходит на внешнем бинаре. |
| FR-5 | Поведение гейта не меняется — покрытие изменённых строк относительно `COVER_BASE`, порог `COVER_MIN` (80), те же исключения. | Прогон даёт тот же вердикт, что и с in-repo-инструментом. |

## 4. Верификация (Definition of Done)

- `internal/covercheck` / `cmd/covercheck` удалены; `grep` не находит импортёров.
- `make tools` ставит `covercheck@v0.1.1`; `covercheck` резолвится и запускается.
- `make ci` зелено сквозь, включая шаг `cover-check` на внешнем бинаре.
- Вердикт diff-покрытия совпадает с поведением до выноса на `COVER_MIN=80`.

## 5. Итог реализации

Приземлено на ветке `ci/coverage-gate-tuning` (свёрнуто с тюнингом `COVER_MIN=80` + Codecov-ignore, по решению владельца «один чистый gobpm PR»):

- Удалены `internal/covercheck/` и `cmd/covercheck/`.
- `Makefile`: `COVERCHECK_VERSION := v0.1.1`; `tools` ставит его; `cover-check` использует установленный бинарь за `require-tool`.
- `.github/workflows/check.yml`: ставит `covercheck@v0.1.1` рядом с прочими закреплёнными инструментами.

Проверено при приземлении: `make tools` ставит `covercheck@v0.1.1`; `make ci` зелено сквозь на внешнем бинаре; дрейфа `go.mod`/`go.sum` нет.

## 6. Ссылки

- [SRD-002 v.1 CI Diff-Coverage Gate](SRD-002-ci-diff-coverage-gate.ru.md) — определяет гейт; этот SRD замещает только его §4.2 (место инструмента).
- [SAD-001 v.1 §6 Quality Attributes](../design/SAD-001-vision-and-architecture.md); [ADR-003 v.1 Module Layout](../design/ADR-003-module-layout.md) — обоснование cohesion/границ.
- `github.com/dr-dobermann/covercheck` (v0.1.1) — вынесенный инструмент.

## История документа

| Версия | Дата | Автор | Изменение |
|---|---|---|---|
| v.1 | 2026-06-08 | Руслан Габитов | Вынос инструмента diff-покрытия в `github.com/dr-dobermann/covercheck` (v0.1.1); gobpm потребляет его как закреплённый dev-инструмент через `make tools`. Замещает SRD-002 §4.2 (in-repo). Семантика/порог гейта без изменений. |
