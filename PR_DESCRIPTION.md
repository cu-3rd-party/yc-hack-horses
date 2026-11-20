# feat(sync): единый sync engine для всех типов нод (Agent B)

Ветка: `feat/sync-engine` (base = master). Зона: `backend/internal/sync/`, `backend/api/gen/`, `backend/internal/transport/grpc/`.

## Что сделано

### 1. Генерация proto (backend/api/gen/)
- `backend/api/gen/gen.go` — корень пакета с директивой `go:generate`:
  `protoc -I../proto --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative ../proto/sync/v1/sync.proto` (запуск из `backend/api/gen`, напр. `go generate ./api/gen/...`).
- Сгенерированный код закоммичен: `api/gen/sync/v1/sync.pb.go`, `sync_grpc.pb.go`
  (protoc-gen-go v1.36.12, protoc-gen-go-grpc v1.6.2).

### 2. Движок internal/sync/ (ADR-0002, 0005, 0009)
- `engine.go` — `Engine`, единый для local/central/desktop/mobile; различия — через
  `storage.EventStore`/`storage.BlobStore` (контракты из `internal/storage`, не переопределены) и конфиг.
  - `Sync(ctx, peer)` — цикл: обмен VectorClock → pull недостающего (`GetMissingEvents` логика на стороне пира через `PullEvents`) → push недостающего (`GetMissingEvents(remote clock)` → `PushEvents` + Ack) → blobs. Раунды до сходимости (default 8).
  - Батчи: `Config.BatchLimit` (default 256; в proto передаётся как `PullEventsRequest.limit`).
  - Идемпотентность: `Append` дедуплицирует по (node_id, sequence) — повторная передача не создаёт дубликатов.
  - Восстановление после обрыва: состояние сессии не хранится — повторный `Sync` с актуальным VectorClock доезжает недостающими.
  - `NodeIdentity{NodeID, Role, AppVersion}` и `PeerChecker` — локальные интерфейсы движка.
- `blobs.go` — blob-синхронизация: `RequestMissingBlobs` → `UploadBlob` чанками
  (`ChunkSize`, default 64KiB), sha256 сверяется после приёма (у движка и на сервере),
  дедупликация по Has/Put; `PullMissingBlobs` для обратного направления. Протокол симметричный — обе ноды запускают Sync.
- `handshake.go` — `Handshake` с проверкой версии протокола (`ErrProtocolVersion`) и защитой от самосоединения.

### 3. gRPC-транспорт internal/transport/grpc/
- `server.go` — полный набор RPC из proto: `Handshake`, `GetNodeInfo`, `ExchangeVectorClock`,
  `PullEvents` (server stream, батчи по limit, флаг `final`), `PushEvents` (client stream → `AckBatch`
  с ack_event_ids/acked_through), `RequestMissingBlobs`, `UploadBlob` (client stream, offset+final),
  `DownloadBlob` (server stream, offset+final, чанки 64KiB). Приём blob проверяет sha256 после сборки.
- `client.go` — адаптер `syncengine.Peer`/`HandshakePeer` поверх сгенерированного клиента; таймауты на RPC (`ClientConfig.Timeout`, default 30s, стримы 4x). Стрим PushEvents живёт до `Close()` синка.
- Изменений в контрактных файлах (domain/event.go, storage/store.go, proto) — нет.

### 4. Тесты
- `internal/sync/fakes_test.go` — in-memory fake-EventStore/BlobStore (дедупликация как в SQL-реализациях). Зависимости от sqlite нет.
- `integration_test.go` — интеграционные поверх bufconn (реальный gRPC + fake-хранилища):
  - `TestSyncBidirectionalNoDuplicates` — события A→B (pushed=10), повторный sync 0/0 и без дубликатов; симметрично B→A.
  - `TestSyncRecoverAfterDisconnect` — обрыв PushEvents после первого батча (частичная доставка), повторный sync доезжает до 7/7.
  - `TestBlobTransferWithHashCheck` — чанковая передача (1024B), контент совпадает, повторная передача не пишет повторно, hash-mismatch отклоняется, DownloadBlob отдаёт те же данные.
- `handshake_test.go` — проверка версии протокола и самосоединения.
- Опциональный сквозной тест на sqlite не добавлен: sqlite-реализация (другой агент) пока не в ветке; fake-слой полностью покрывает контракт.

## Команды проверки
```
cd backend
export PATH=$HOME/go/bin:$PATH GOCACHE=$HOME/.cache/go-build-shared GOMODCACHE=$HOME/go/pkg/mod
go generate ./api/gen/...   # регенерация proto-кода
go build ./... && go vet ./...
gofumpt -l .                # чисто по моей зоне (см. замечание 2)
go test ./internal/...
```

## Результаты тестов (Go 1.26.0, linux/amd64)
```
ok  github.com/cu3rd/yc-hack-horses/backend/internal/sync        0.03s  (4 теста, -count=2 стабильно)
go vet ./...  — чисто
go build ./... — чисто
```

## Замечания
1. Пакет `internal/sync` называется `sync` (как ADR); при импорте использовать алиас `syncengine`, если в файле нужен stdlib `sync`.
2. `gofumpt -l .` показывает `internal/domain/event.go` — это контрактный файл другой зоны, форматирование не трогал (в файлах своей зоны gofumpt чисто).
3. Направление blob-загрузки: список «что у пира отсутствует» движок получает, отправляя свои хэши через `RequestMissingBlobs`; полный список хэшей ноды даёт `Config.KnownBlobHashes` (nil = blob-sync off). Недостающие у себя нода забирает `PullMissingBlobs` по своим кандидатам — полный перечень «всех blob пира» протоколом не предусмотрен.
4. Сервер PullEvents не отдаёт пустой final-батч, если недостающих нет (клиент получает EOF сразу) — движок трактует это как «нечего тянуть».
