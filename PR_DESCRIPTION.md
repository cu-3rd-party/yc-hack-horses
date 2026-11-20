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
