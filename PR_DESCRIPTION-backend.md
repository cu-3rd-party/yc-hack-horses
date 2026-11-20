<<<<<<< HEAD
# PR: Frontend (React) + Desktop (Tauri) — клиентская часть V0

Ветка: `feat/frontend-desktop`. Зона: `app/`, `packages/contracts/`, `desktop/`. Backend/infra не затронуты.

## Что сделано

### packages/contracts/
Ручные TS-типы по API-контракту ноды (единый источник): `Horse`, `Observation`, `TraitSnapshot`, `Blob`, `GeoLocation`, `NodeStatus` (+ storage/certificate/peers), payload-типы для POST/PATCH, `WsEvent` для WS `/api/v1/events`. Пакет `@yc-hack-horses/contracts` подключён в app через `file:../packages/contracts`.

### app/ — React + TS strict + Vite + Tailwind + shadcn/ui
FSD-структура:
- `src/app` — root (Router + layout)
- `src/pages` — 6 экранов: Dashboard, Horses, Observations, New Observation, Sync, Settings
- `src/widgets` — sidebar-навигация (с индикатором офлайн-очереди)
- `src/features` — offline-queue (localStorage, flush-исполнители), new-observation (save/inference-стаб), sync-now
- `src/entities` — horse/observation: display-хелперы (имя, статус identity, форматирование)
- `src/shared` — api (typed fetch wrapper `ApiClient`, `ApiError`/`NodeUnavailableError`, endpoints, hooks: `useNodeResource`, `useNodeEvents` с reconnect), ui (Button/Card/Badge/Input/Label/Select/Textarea в shadcn-стиле на radix+cvа), lib, config

Границы FSD проверяются ESLint: `import/no-restricted-paths` запрещает нижним слоям импортировать вышележащие (shared ← entities ← features ← widgets ← pages ← app). `npm run lint` чистый.

Экраны:
- **Dashboard**: статус ноды (id/роль/версия/uptime), central-соединение (peer state, last sync, ошибка), счётчики animals/observations/pending events + размер хранилища, статус WS-стрима событий.
- **Horses**: список + карточка (metadata, identity_status, раскрытие истории наблюдений).
- **Observations**: список (наблюдатель, нода, локация, блобы) + фильтр «без привязки».
- **New Observation**: выбор фото (мультизагрузка), кнопка локального inference (стаб: недоступен → ручной ввод), ручной ввод признаков с confidence, геолокация (navigator.geolocation или ручной ввод lat/lon), привязка к лошади или unknown, сохранение через multipart `/api/v1/blobs` → POST `/api/v1/observations`; при недоступной ноде — офлайн-очередь.
- **Sync**: список пиров (state, last sync, счётчики событий, ошибка), кнопка «Sync now» (POST /api/v1/node/sync), очередь событий ноды.
- **Settings**: node id, роль, статус сертификата/доверия (valid/trust anchor, срок), хранилище (движок/размер/счётчики). Приватные ключи не отображаются.

Offline-first: всё работает против `127.0.0.1:8080` (локальная нода); нода недоступна → баннер «offline» и пустые состояния вместо падения; неотправленные действия копятся в очереди (localStorage) и выгружаются при возврате связи, статус очереди виден в сайдбаре.

### desktop/ — Tauri v2
- `src-tauri/`: Cargo.toml (tauri 2, serde, libc), `tauri.conf.json` (frontendDist → `../../app/dist`), capabilities, `src/main.rs`, `src/sidecar.rs`.
- Sidecar: запуск бинаря ноды (`YC_NODE_BIN` или `<exe>/bin/node`, порт `YC_NODE_PORT`/8080), ожидание готовности GET `/health` на 127.0.0.1 (std TcpStream, таймаут 30 c), супервизор-поток с рестартом при падении (задержка 3 c), статус для UI командой `node_sidecar_status`.
- Строгое завершение: `pre_exec(PR_SET_PDEATHSIG, SIGKILL)` в дочернем процессе + kill/wait и join супервизора в `RunEvent::Exit` и в `Drop` — процесс ноды не переживает Tauri.
- UI: тот же React-бандл из `app/dist` (MemoryRouter — без адресной строки).

## Как проверить

```bash
# Frontend
cd app
export PATH=$HOME/.nvm/versions/node/v24.12.0/bin:$PATH   # node >= 20
npm ci --cache $HOME/.npm-shared
npm run build        # tsc -b + vite build
npm test             # vitest: офлайн-очередь (4 теста) + утилиты (5 тестов)
npm run lint         # FSD-границы + TS-правила

# Desktop (ограничение CPU)
cd ../desktop/sidecar && taskset -c 0-3 env CARGO_BUILD_JOBS=4 cargo check && cargo test
# Полная проверка src-tauri (нужны системные пакеты Ubuntu 22.04+):
#   apt-get install -y libgtk-3-dev libwebkit2gtk-4.1-dev libayatana-appindicator3-dev librsvg2-dev
cd ../src-tauri && taskset -c 0-3 env CARGO_BUILD_JOBS=4 cargo check
```

## Результаты проверок

- `npm install`/`npm ci`: OK (414+39 пакетов).
- `npm run build`: OK — dist 220.8 KB js / 10.5 KB css.
- `npm test`: 9 passed (2 файла).
- `npm run lint`: 0 ошибок.
- `cargo check` + `cargo test` (desktop/sidecar): OK, 3 теста passed.
- `cargo check` (desktop/src-tauri): **заблокирован средой** — хост Ubuntu 20.04 не имеет `libwebkit2gtk-4.1-dev` (нужен для Tauri v2/wry; в focal есть только 4.0). На Ubuntu 22.04+ достаточно apt-строки выше; остальные зависимости (tauri 2, serde_json, path-крейт sidecar) резолвятся.

## Замечания

- **Структура desktop**: sidecar-менеджмент вынесен в отдельный крейт `desktop/sidecar` (без зависимости от tauri) — он полностью проверяется `cargo check`/`cargo test` независимо от GUI-зависимостей; `desktop/src-tauri` зависит от него по path.
- Inference — стаб (ADR-0012 не стабилизирован): при `available=false` UI предлагает ручной ввод.
- Эндпоинт ручной синхронизации принят как `POST /api/v1/node/sync`; если бэкенд назовёт иначе — правка в `features/sync-now/model/sync-now.ts` (одна строка).
- `NodeStatus.peers`/`certificate` — справочная структура по контракту; поля опциональны, UI терпим к отсутствию.
=======
# PR: Backend Foundation + Storage (feat/backend-foundation)

Ветка: `feat/backend-foundation` (base: master). Remote: `ssh://git@git.cu3rd.ru:2222/cu3rd/yc-hack-horses`.

## Что сделано

- **Storage**: единый SQL-движок `internal/storage/sqlstore` (database/sql; sqlite = modernc.org/sqlite pure-Go, postgres = pgx/v5 stdlib), тонкие пакеты-драйверы `internal/storage/sqlite` и `internal/storage/postgres`. Реализован контракт `storage.EventStore` из master (Append идемпотентен по (node_id, sequence) через `ON CONFLICT DO NOTHING`, GetEvents по Cursor, GetVectorClock, GetMissingEvents, LastSequence) + `RunInTxEvents` для транзакционной записи.
- **BlobStore**: `internal/storage/fsblob` — content-addressed ФС, пути `blobs/<ab>/<hex62>`, дедупликация по sha256, атомарная запись (tmp+rename), проверка фактического хэша при Put, Put/Get/Has/Delete/ListMissing/Size, ошибки через `storage.ErrNotFound`.
- **Миграции**: `backend/migrations/{sqlite,postgres}/001_init.sql`, раннер `internal/storage/migrate` (версионный, идемпотентный, реестр в `schema_migrations`). Таблицы: `events`, `horses`, `observations`, `trait_snapshots`, `blobs`; индексы по `event_type`, `aggregate_id`, `occurred_at`, уникальность `(node_id, sequence)`; в postgres `payload`/`metadata`/`blob_ids` — JSONB.
- **REST API** (`internal/transport/http`, chi): `/health`, `/readyz`, `/api/v1/horses` (GET/POST/PATCH/GET{id}), `/api/v1/observations` (GET/POST), `/api/v1/blobs` (POST multipart «file», GET список, GET по sha256 — raw + ETag), `/api/v1/node`, WS `/api/v1/events` (первый кадр — векторные часы, далее `{"type":"event","event":…}`). Записи идут как событие журнала + проекция в одной транзакции (`RunInTx`); публикация в WS — после коммита.
- **CLI** (`cmd/node`, urfave/cli/v3): `web` (default) и `migrate`; graceful shutdown по SIGINT/SIGTERM; конфиг: значения по умолчанию как в config.example.yaml → YAML (`--config/-c`) → env (`NODE_ID`, `DATA_DIR`, `STORAGE_DRIVER`, `DATABASE_URL`, `LISTEN_ADDR`, `GRPC_LISTEN_ADDR`, `SYNC_*`, `LOG_LEVEL`, …); валидация драйвера/адресов/интервалов. node_id генерируется (UUIDv7) и сохраняется в `<data_dir>/node.json`.
- **Dockerfile**: multi-stage, CGO disabled, distroless `nonroot`, миграции копируются в образ, `CMD ["web"]`.
- Контракты из master не менялись (`internal/domain/event.go`, `internal/storage/store.go`, `api/proto/...`); `sync/`, `security/`, `identity/`, `inference/`, `api/` не тронуты.

## Изменённые интерфейсы (новое поверх контрактов master)

- `sqlstore.Store`: `RunInTx(ctx, func(*Store) error)` и `RunInTxEvents(ctx, func(storage.EventStore) error)`; проекции `CreateHorse/GetHorse/ListHorses/UpdateHorse/CreateObservation/GetObservation/ListObservations/CreateTraitSnapshot/UpsertBlob/ListBlobs/GetBlobMeta/NodeSummary/Ping/DB`.
- `migrate.Apply(ctx, db, migrationsDir, driver)`.
- Событие правки лошади: тип `horse.updated` (константа в хендлерах, payload: `identity_status`) — доменные константы master не расширялись.

## Команды проверки

```
cd backend
go build ./... && go vet ./... && gofumpt -l .   # чисто (кроме pre-existing форматирования internal/domain/event.go — контракт, не трогал)
go test ./...                                     # unit + contract sqlite + integration
TEST_PG_DSN='postgres://postgres:test@127.0.0.1:5432/test?sslmode=disable' go test ./internal/storage/postgres/
docker build -t yc-hack-horses-node:test .
docker run --rm -p 8080:8080 yc-hack-horses-node:test
```

## Результаты тестов (последний прогон)

- `internal/config` — ok (defaults, YAML+env override, валидация)
- `internal/storage/sqlite` — ok: contract EventStore (8 кейсов) + BlobStore (6 кейсов)
- `internal/storage/postgres` — ok: тот же контрактный набор (postgres:16 в docker)
- `tests/integration` — ok: health/readyz, JSON 404, lifecycle horses, observations, blobs (upload/dedup/get), node info, WS-стрим

## Замечания

- `GetMissingEvents` ограничен 10000 событий за вызов (без параметра лимита в контракте).
- SQLite работает с `MaxOpenConns=1` + WAL — исключает SQLITE_BUSY; для нагруженного central в перспективе мигрировать на bufpool или postgres.
- `fmt.Print*` не используется (slog), panic только на программном инварианте маршалинга payload.
>>>>>>> origin/feat/backend-foundation
