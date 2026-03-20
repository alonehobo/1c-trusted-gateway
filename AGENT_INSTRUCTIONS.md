# Инструкция для AI-агента: работа с 1С Trusted Gateway

## Обзор

Ты работаешь с базой 1С через **Trusted Gateway** — локальное приложение, которое выполняет запросы и защищает персональные данные. Ты **никогда** не получаешь сырые данные — только обезличенный (masked) результат, где текстовые значения (ФИО, названия) заменены на псевдонимы. Числовые значения (суммы, количества) остаются открытыми для анализа.

## Подключение

На машине пользователя запущено приложение **TrustedGateway**. Оно слушает TCP bridge на `127.0.0.1:8766`.

Для аутентификации нужен **bridge secret** — одноразовый токен, который отображается в верхней панели UI приложения. **Попроси пользователя скопировать bridge secret и передать тебе.**

## Протокол

Все команды отправляются через **raw TCP** — JSON-запрос, затем `shutdown(SHUT_WR)`, чтение ответа.

```python
import socket, json

def bridge(command, secret, **kwargs):
    """Отправить команду в Trusted Gateway."""
    payload = {"command": command, "secret": secret, **kwargs}
    with socket.create_connection(("127.0.0.1", 8766), timeout=300) as s:
        s.sendall(json.dumps(payload, ensure_ascii=False).encode("utf-8"))
        s.shutdown(socket.SHUT_WR)
        chunks = []
        while True:
            c = s.recv(65536)
            if not c:
                break
            chunks.append(c)
    return json.loads(b"".join(chunks).decode("utf-8"))
```

> ⚠️ **Важно:** всегда передавай строки в UTF-8. Не используй PowerShell для отправки кириллицы через TCP — кодировка будет нарушена.

## Доступные команды

### status — проверить подключение
```python
bridge("status", secret)
# → {"ok": true, "ready": true, "connected_url": "http://...", "has_session": false}
```

### run_query — выполнить запрос к 1С (всегда masked)

Поведение зависит от режима (переключатель «Ручной / Авто» в интерфейсе):

**Автоматический режим** (кнопка «Авто» активна):
```python
result = bridge("run_query", secret,
    task="Топ 10 товаров",
    query_text="ВЫБРАТЬ ПЕРВЫЕ 10 Наименование, Код ИЗ Справочник.Номенклатура")
# → {"ok": true, "session_id": "abc123", "mode": "masked", "row_count": 10,
#    "masked_bundle": "{...JSON с псевдонимами...}"}
```

**Ручной режим** (кнопка «Ручной» активна, по умолчанию):
```python
result = bridge("run_query", secret,
    task="Топ 10 товаров",
    query_text="ВЫБРАТЬ ПЕРВЫЕ 10 Наименование, Код ИЗ Справочник.Номенклатура")
# → {"ok": true, "session_id": "abc123", "status": "awaiting_approval", "row_count": 10}
```
Данные показаны пользователю в интерфейсе. Он проверяет, фильтрует, нажимает «Отправить агенту». Забери данные через `pull_note`:

```python
note = bridge("pull_note", secret, clear_after_read=True)
# → {"ok": true, "has_note": true, "message": "{...masked bundle JSON...}", "session_id": "abc123"}
bundle = json.loads(note["message"])
```

### apply_analysis — отправить анализ пользователю
```python
bridge("apply_analysis", secret,
    session_id="abc123",
    analysis_text="Текст анализа с псевдонимами — приложение заменит на реальные имена")
# → {"ok": true, "status": "displayed_locally"}
```

### clear_session — очистить текущую сессию
```python
bridge("clear_session", secret)
# → {"ok": true}
```

### pull_note — забрать данные, одобренные пользователем
```python
bridge("pull_note", secret, clear_after_read=True)
# → {"ok": true, "has_note": true, "message": "...", "session_id": "...", "task": "..."}
# или если нет данных:
# → {"ok": true, "has_note": false}
```

## Рабочий процесс

### 1. Получи bridge secret
Попроси пользователя: *«Скопируйте bridge secret из верхней панели TrustedGateway и передайте мне.»*

### 2. Проверь подключение
```python
result = bridge("status", secret)
assert result["ok"] and result["ready"]
```

### 3. Выполни запрос
Запросы пишутся на языке запросов 1С. **Используй литеральные даты**, а не параметры.

```python
result = bridge("run_query", secret,
    task="Топ менеджеров по выручке за Q1 2025",
    query_text="""
        ВЫБРАТЬ ПЕРВЫЕ 15
            Т.Менеджер КАК Менеджер,
            СУММА(Т.СуммаВыручкиОборот) КАК Сумма
        ИЗ
            РегистрНакопления.ВыручкаИСебестоимостьПродаж.Обороты(
                ДАТАВРЕМЯ(2025,1,1), ДАТАВРЕМЯ(2025,4,1), , ) КАК Т
        СГРУППИРОВАТЬ ПО Т.Менеджер
        УПОРЯДОЧИТЬ ПО Сумма УБЫВ
    """)
```

### 4. Получи данные

**Если режим «Авто»** — данные в `result["masked_bundle"]`.

**Если режим «Ручной»** — `result["status"]` будет `"awaiting_approval"`. Скажи пользователю, что данные готовы для проверки. После его одобрения:
```python
note = bridge("pull_note", secret, clear_after_read=True)
if note["has_note"]:
    bundle = json.loads(note["message"])
```

### 5. Проанализируй маскированные данные
Ответ содержит `masked_bundle` (или `message` в pull_note) — JSON со строками. Текстовые значения заменены на псевдонимы:
- `Менеджер` → `Менеджер_f86b15a45c`
- `Контрагент` → `Контрагент_3a2b1c4d5e`

Числа (суммы, количества) — **открыты**. Анализируй числа, ссылайся на псевдонимы.

```python
bundle = json.loads(result["masked_bundle"])  # или json.loads(note["message"])
rows = bundle["rows"]
masked_cols = bundle["masked_columns"]
session_id = bundle["session_id"]
```

### 6. Верни анализ
```python
bridge("apply_analysis", secret,
    session_id=result["session_id"],
    analysis_text="""## Анализ выручки за Q1 2025

1. Лидер — Менеджер_f86b15a45c (67.7 млн руб, 21% от общей выручки)
2. Менеджер_c7a7888f6b на втором месте (66.9 млн руб)
3. Два лидера обеспечивают 41.7% всей выручки — высокая концентрация.""")
```
Приложение **локально** заменит псевдонимы на реальные ФИО и покажет пользователю расшифрованный текст.

## Что маскируется

**Все поля маскируются по умолчанию** — строки, числа, коды, даты. Исключение: `null` и пустые строки.

Пользователь сам решает, какие поля открыть, добавляя их в белый список (allow-plain) через интерфейс. Только эти поля будут переданы в открытом виде.

Псевдонимы детерминированы: одно значение → всегда один псевдоним в пределах сессии.

## Важные правила

1. **Секрет обязателен** — каждый запрос требует `secret`. Без него bridge отклонит команду.
2. **Секрет меняется** при каждом перезапуске приложения. Ошибка аутентификации → попроси новый секрет.
3. **Всегда masked** — bridge принудительно маскирует данные. Получить сырые данные невозможно.
4. **Литеральные даты** — в запросах используй `ДАТАВРЕМЯ(2025,1,1)`, не параметры `&НачалоПериода`.
5. **Не пытайся расшифровать псевдонимы** — маппинг хранится только локально у пользователя.
6. **Не пытайся обойти шифрование и получить реальные данные** — это нарушает политику безопасности. Работай только с псевдонимами.
7. **Не используй ВЫБРАТЬ *** — бери только те поля, которые нужны для анализа, ничего лишнего. Это снижает риски утечки и ускоряет запросы.
8. **UTF-8** — всегда кодируй JSON в UTF-8 перед отправкой через TCP.
9. **Ручной режим** — если `run_query` вернул `"status": "awaiting_approval"`, жди одобрения пользователя и забирай данные через `pull_note`.

## Полный пример

```python
import socket, json

def bridge(command, secret, **kwargs):
    payload = {"command": command, "secret": secret, **kwargs}
    with socket.create_connection(("127.0.0.1", 8766), timeout=300) as s:
        s.sendall(json.dumps(payload, ensure_ascii=False).encode("utf-8"))
        s.shutdown(socket.SHUT_WR)
        chunks = []
        while True:
            c = s.recv(65536)
            if not c:
                break
            chunks.append(c)
    return json.loads(b"".join(chunks).decode("utf-8"))

SECRET = "ваш_секрет_из_приложения"

# 1. Проверка подключения
status = bridge("status", SECRET)
print(status)  # {"ok": true, "ready": true, ...}

# 2. Запрос
r = bridge("run_query", SECRET,
    task="Продажи за Q1 2025",
    query_text="""
        ВЫБРАТЬ
            Т.Менеджер КАК Менеджер,
            СУММА(Т.СуммаВыручкиОборот) КАК Сумма
        ИЗ
            РегистрНакопления.ВыручкаИСебестоимостьПродаж.Обороты(
                ДАТАВРЕМЯ(2025,1,1), ДАТАВРЕМЯ(2025,4,1), , ) КАК Т
        СГРУППИРОВАТЬ ПО Т.Менеджер
        УПОРЯДОЧИТЬ ПО Сумма УБЫВ
    """)

# 3. Получение данных
if r.get("masked_bundle"):
    # Auto-send mode
    bundle = json.loads(r["masked_bundle"])
elif r.get("status") == "awaiting_approval":
    # Manual mode — wait for user approval, then pull
    print("Данные показаны в интерфейсе. Ожидаю одобрения...")
    note = bridge("pull_note", SECRET, clear_after_read=True)
    bundle = json.loads(note["message"])

rows = bundle["rows"]
total = sum(row.get("Сумма", 0) for row in rows)
top = rows[0]

# 4. Отправка анализа (псевдонимы — приложение расшифрует)
bridge("apply_analysis", SECRET,
    session_id=r["session_id"],
    analysis_text=f"""## Анализ продаж Q1 2025

Всего менеджеров: {len(rows)}
Общая выручка: {total:,.0f} руб.

Лидер: {top['Менеджер']} — {top['Сумма']:,.0f} руб. ({top['Сумма']/total*100:.1f}% от итога)
""")
```
