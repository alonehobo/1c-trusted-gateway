# Паттерны и соглашения проекта

<!--
Сюда заноси только устойчивые правила, которые стоит применять повторно:
- именование;
- структура модулей или пакетов;
- соглашения по API;
- правила ошибок и логирования;
- тестовые паттерны;
- принятые UI- или DX-конвенции.
-->

Пока устойчивые паттерны не зафиксированы.

## GitHub — только через API

`git push` не работает (broken HTTPS helper, SSH pack fails). Все операции с GitHub — через `gh api` или `gh` CLI:

- **Пуш коммитов**: создать blobs → tree → commit → PATCH ref через `gh api`
- **Релизы**: `gh release upload <tag> <file> --repo alonehobo/1c-trusted-gateway`
- **Создание релиза**: `gh api repos/.../releases --input payload.json`
- Всегда использовать `--input <file>` для больших payload (ui.html, web.go не влезают в аргументы командной строки на Windows)

## Работа со шлюзом (тестирование, отладка)

Перед работой со шлюзом читать `README.md` (раздел TCP Bridge API) и `AGENT_INSTRUCTIONS.md` самостоятельно — не спрашивать у пользователя базовые вещи вроде порта или формата команд.

- TCP bridge: `127.0.0.1:8766`, протокол JSON → `shutdown(SHUT_WR)` → JSON
- Единственное что нужно спрашивать — bridge secret (генерируется при каждом запуске)
- Команды: `status`, `run_query`, `pull_note`, `apply_analysis`, `clear_session`
