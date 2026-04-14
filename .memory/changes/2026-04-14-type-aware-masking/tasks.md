# Tasks

## Discovery и спецификация

- [x] Зафиксировать цель, границы и критерии готовности.
- [x] Уточнить ключевые компоненты, сценарии и ограничения.
- [x] Написать `proposal.md`, `design.md`, `spec-delta.md`.
- [x] Согласовано пользователем через «сделай задачи по спецификации» (сессия 2026-04-14).

## Уже сделано (Задача 2, вне этого change)

- [x] Параметр `IncludeSchema` на стороне 1С MCP (`MCP from 1C/src/DataProcessors/mcp_ИнструментЗапросыКБазе/Ext/ManagerModule.bsl`).
- [x] JSON-формат ответа `{version, columns[{name,types,truncated?,types_total?}], rows}`.
- [x] Лимит 10 типов на колонку с маркерами `truncated`/`types_total`.
- [x] Компиляция через `compile_check` — OK. (Smoke на реальной базе — остаётся.)

## Реализация — Задача 1: strip `ПРЕДСТАВЛЕНИЕ()`

- [x] Новая функция `stripPresentationCalls(query string) string` в `query_normalize.go`.
- [x] Подключить `stripPresentationCalls` в `mcp_server.go:toolQuery` перед `normalizeQueryAliases`.
- [x] Подключить в `web.go:HandleQuery` (UI-путь) — симметрично.
- [x] Юнит-тесты в `query_normalize_test.go` (16 кейсов + интеграционный).

## Реализация — Задача 3: type-aware маскирование

### Клиент и парсер

- [x] В вызове `query` добавлен `IncludeSchema: true`.
- [x] `extractRowsWithSchema` в `service.go` парсит JSON со схемой; fallback на TSV.
- [x] Тип `ColumnSchema struct { Name, Types, Truncated, TypesTotal }`.

### Сессия

- [x] `TrustedSession` дополнен `ColumnSchemas`, `ColumnTypes`, `ColumnTruncated`.
- [x] Заполняются при успешном приёме; передаются в sanitizer при remask.

### Type policy

- [x] `type_policy.go` с `TypePolicy.Decide(types, truncated) Decision`.
- [x] Дефолты: префиксы `Перечисление./ПланСчетов./ПланВидовХарактеристик.`; точные `Справочник.Валюты/СтавкиНДС/СтраныМира/БанкиРФ`; примитивы `Число/Дата/Булево/УникальныйИдентификатор`.
- [x] `MergePersisted` сливает user-политику с дефолтами.
- [x] `type_policy_test.go` — 17 кейсов (decide, merge, forced priority, invalid JSON).

### Sanitizer

- [x] В `privacy.go:maskValue` type-policy шаг между force-mask и allow-plain.
- [x] `DataSanitizer` получает `typePolicy`, `columnTypes`, `columnTruncated`.
- [x] `privacy_test.go` — 5 кейсов (integration, truncated, unknown fallback, force-mask priority, schema parser).

### Персистентная политика

- [x] `PersistentTypePolicy string` в `TrustedWebApp`.
- [x] Сохранение в `settings.bin` под ключом `type_policy`; загрузка при старте в `main.go`.
- [x] HTTP-хэндлер `POST /api/set_type_policy`; `HandleSetTypePolicy` в `web.go`.

### UI

- [x] Sub-tab «Политика типов» в настройках. Таблица (Тип/Префикс, Политика, удалить).
- [x] Кнопки «+ Plain», «+ Mask», «Сбросить к дефолтам», «Применить».
- [x] Синхронизация `type_policy_effective` из стейта (только когда таб не активен).
- [ ] _Отложено (не блокирует MVP)_: отображение типа колонки в шапке таблицы результатов.
- [ ] _Отложено_: бейдж `truncated: N` в шапке таблицы.

## Финальный test gate

- [x] `go test ./...` — зелёный (все пакеты).
- [x] `go vet ./...` — чисто.
- [x] Бинарь собран (`go build -ldflags "-H=windowsgui"`).
- [ ] Manual smoke: реальный запрос через MCP от агента на базе пользователя.
- [ ] Manual smoke: UI-путь — запустить запрос из UI, проверить политику типов, персистентность.
- [x] Обновлён `CHANGELOG.md` в `.memory/`.
- [ ] Передать результат пользователю на приёмку smoke.

## Последовательность выполнения

Выполнено за одну сессию. Порядок:
1. Задача 1 целиком + тесты.
2. Задача 3 слоями: type_policy → client/parser → session → sanitizer → persistence → UI.
3. `go test ./...`, `go vet`, сборка.
