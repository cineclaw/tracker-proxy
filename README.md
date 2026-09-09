# CineClaw Tracker Proxy

[![Docker Image](https://github.com/cineclaw/tracker-proxy/actions/workflows/docker-publish.yml/badge.svg)](https://github.com/cineclaw/tracker-proxy/actions/workflows/docker-publish.yml)
[![Version](https://img.shields.io/badge/version-1.0.0-blue.svg)](https://github.com/cineclaw/tracker-proxy/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

**CineClaw Tracker Proxy** — высокопроизводительный агрегатор торрент-трекеров, движок дедупликации и FUSE-оркестратор на Go для домашнего кинотеатра CineClaw.

Сервис объединяет раздачи с **RuTracker**, **RuTor** и **NNM-Club**, выполняет интеллектуальную дедупликацию по InfoHash, объединяет сидов со всех площадок и оркестрирует мгновенное виртуальное монтирование в Tiramisu FUSE и Jellyfin.

---

## Архитектура и возможности

1. **Мульти-трекерный скрейпинг**:
   - **RuTor.info**: Прямой скрейпинг без авторизации с нормализацией поисковых запросов.
   - **RuTracker.org**: Интеграция с FlareSolverr для прозрачного прохождения защиты Cloudflare Turnstile и сохранения cookie-сессии (`bb_session`).
   - **NNM-Club.to**: Строгое ограничение параллелизма (семафор $\le 2$, задержка $\ge 75$ мс) и перекодирование Windows-1251 $\to$ UTF-8.
2. **Кросс-трекерная дедупликация**:
   - Кластеризация по размеру раздачи с допуском strictly $\pm 0.105$ ГБ ($\pm 0.1$ ГБ при округлении до десятых).
   - Сравнение InfoHash из bbolt кэша (`topic_hashes`) и topic-страниц.
   - Слияние дубликатов в единую карточку: суммирование сидов ($\sum \text{seeds} = \text{seeds}_{\text{RuTracker}} + \text{seeds}_{\text{RuTor}} + \text{seeds}_{\text{NNM}}$) и синтез единой мульти-трекерной magnet-ссылки со всеми announce-серверами.
3. **Оркестрация виртуального стриминга (Tiramisu FUSE + Jellyfin)**:
   - Регистрация magnet в GoStorm API Tiramisu (`POST /torrents`).
   - Ожидание метаданных торрента и генерация легковесных виртуальных `.mkv` JSON stubs (~150 байт) в `/media/source/`.
   - Запрос метаданных серий из `imdb-indexer` и генерация полных XML `.nfo` (кино, сериалы, эпизоды) для мгновенного распознавания в Jellyfin без внешних обращений к базам.
   - Таргетированный сброс кэша Jellyfin (`POST /Items/{id}/Refresh`) без 60-секундной задержки LibraryMonitor.
4. **Двустороннее размонтирование и автоочистка**:
   - `POST /api/stream/unmount`: мгновенное удаление stubs и unmount торрента из памяти.
   - Обработка вебхука Jellyfin `ItemDeleted` для автоматической очистки stubs при удалении пользователем из интерфейса плеера.

---

## Структура проекта

```
tracker-proxy/
├── cmd/
│   └── server/             # Точка входа main.go
├── config/                 # Конфигурация и переменные окружения
├── pkg/
│   ├── aggregator/         # Оркестратор поиска, размерная кластеризация и дедуп
│   ├── api/                # HTTP обработчики (/torrents, /health, /api/stream/*)
│   ├── cache/              # Встроенная база bbolt (imdb_cache, topic_hashes)
│   ├── models/             # Модели данных раздач и стримов
│   ├── stream/             # FUSE-оркестратор Tiramisu, генератор NFO и Jellyfin API
│   ├── tracker/            # Интерфейс трекера
│   ├── trackers/           # Реализации скрейперов (rutor, rutracker, nnmclub)
│   └── version/            # Версионирование SemVer
├── Dockerfile              # Multi-stage сборка Golang -> Alpine
└── go.mod                  # Зависимости Go
```

---

## Запуск в разработке

### Требования
- Go 1.22+

```bash
# Установка зависимостей
go mod download

# Запуск сервера на порту 9118
go run ./cmd/server
```

---

## Переменные окружения

| Переменная | Описание | По умолчанию |
| :--- | :--- | :--- |
| `CONFIG_PATH` | Путь к YAML-конфигурации | `/app/config.yaml` |
| `FLARESOLVERR_URL` | URL инстанса FlareSolverr | `http://flaresolverr:8191/v1` |
| `TIRAMISU_URL` | URL Tiramisu GoStorm API | `http://tiramisu:8090` |
| `JELLYFIN_URL` | URL сервера Jellyfin | `http://jellyfin:8096` |
| `JELLYFIN_API_KEY` | API-ключ Jellyfin | `a4151fee9ef64ea6b23b185f8fe2720e` |
| `IMDB_INDEXER_URL` | URL сервиса imdb-indexer | `http://imdb-indexer:8090` |
| `RUTRACKER_USERNAME` | Логин на RuTracker (опционально) | `""` |
| `RUTRACKER_PASSWORD` | Пароль на RuTracker (опционально) | `""` |
| `NNMCLUB_USERNAME` | Логин на NNM-Club (опционально) | `""` |
| `NNMCLUB_PASSWORD` | Пароль на NNM-Club (опционально) | `""` |

---

## Docker-контейнер

Готовый многоплатформенный образ публикуется в GitHub Container Registry:
```bash
docker pull ghcr.io/cineclaw/tracker-proxy:latest

# Запуск
docker run -d \
  -p 9118:9118 \
  -v $(pwd)/data/tracker-proxy/config.yaml:/app/config.yaml \
  -v $(pwd)/data/tracker-proxy/cache:/app/cache \
  ghcr.io/cineclaw/tracker-proxy:latest
```

---

## HTTP API

- `GET /health` — проверка здоровья сервиса и статус с версией (`{"status": "ok", "version": "1.0.0"}`).
- `GET /api/torrents?tconst=tt...` — поиск, агрегация и дедупликация раздач по IMDb ID.
- `POST /api/stream/mount` — монтирование выбранного релиза в виртуальную систему FUSE и Jellyfin.
- `POST /api/stream/unmount` — размонтирование релиза и удаление stubs.
- `GET /api/stream/status?tconst=tt...` — статус монтирования текущего тайтла.
- `POST /api/stream/webhook/deleted` — обработчик вебхука Jellyfin `ItemDeleted`.

---

## Лицензия
MIT License.
