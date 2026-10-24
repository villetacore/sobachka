# Разработка

## Запуск без Docker

```bash
# ML
cd ml && python -m venv .venv && .venv/bin/pip install -r requirements.txt
MODEL_DIR=./models .venv/bin/uvicorn app.main:app --port 8000

# backend (нужен PostgreSQL)
cd backend && DATABASE_URL=postgres://... ML_URL=http://localhost:8000 ADMIN_USERNAME=admin ADMIN_PASSWORD=... APP_SECRET_KEY=... PUBLIC_URL=http://localhost:5173 COOKIE_SECURE=false go run ./cmd/server

# web (проксирует /api на :8080)
cd web && npm install && npm run dev
```

Тесты: `cd backend && go test ./...`; проверка готовых площадок из файла на живых сайтах: `PRESETS_FILE=$PWD/../config/presets.json LIVE=1 go test ./internal/sources -run Live -v`.

## Структура

```
backend/            Go: сбор заказов, очередь, PostgreSQL, Telegram, REST API
  internal/sources  универсальные источники rss, html, json; config.go — настройки разбора; готовые площадки — из файла
  internal/core     конвейер: оценка → отклик → уведомление, дообучение; pitch.go — шаблоны откликов
  internal/store    БД; seed.go — стартовые профили и источники
  internal/api      API для веб-интерфейса (сессии, роли, CSRF)
ml/app/scorer.py    ключевые слова + своя нейросеть на PyTorch и её самообучение
web/                React + TypeScript (Vite), раздаётся nginx
```

## Тесты и проверки безопасности

`cd backend && go test ./...` запускает unit-тесты. Для интеграционных тестов задайте `TEST_DATABASE_URL` на отдельную тестовую PostgreSQL: тесты создают временные схемы и удаляют только свои схемы. Не указывайте рабочую базу. Проверяются вход/выход, истечение и отзыв сессий, роли API, CSRF, ограничения попыток, смена пароля, защита последнего администратора, одноразовые привязки, подписки, повторная доставка и права на кнопки Telegram. В тестах Telegram заменён имитацией: реальных сообщений они не отправляют.

```bash
TEST_DATABASE_URL=postgres://postgres:password@localhost:55439/postgres?sslmode=disable go test ./...
# интерфейс
cd ../web && npm run build
```

Подтверждённые в БД доставки повторно не отправляются. Если Telegram принял сообщение, а процесс остановился до записи результата в БД, при повторе возможен дубль — Telegram Bot API не предоставляет ключ идемпотентности для отправки.

Схема привязки соответствует [Telegram deep linking](https://core.telegram.org/bots/features#deep-linking).
