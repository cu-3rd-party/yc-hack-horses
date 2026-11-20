# feat/node-wiring — Node Wiring и E2E (Agent F)

Связывает компоненты ноды в работающее целое и доказывает сквозной sync.
(Файл-сиблинг чужих PR_DESCRIPTION.md — их не трогаю.)

## Схема запуска (`node web`)

1. **HTTP REST/WS** — `network.listen_addr` (chi, прежние маршруты
   `/health`, `/readyz`, `/api/v1/horses|observations|blobs|node|events`).
2. **gRPC SyncService** — `network.grpc_listen_addr`:
   - при сконфигурированных `security.cert_file/key_file` — mTLS
     (`internal/security.LoadServerTLSConfig`): клиент обязан предъявить
     доверенный серт с URI SAN `przewalski://node/<uuid>`;
   - без сертификатов — plain-режим и **только loopback**: попытка слушать
     plain gRPC вне loopback завершает запуск ноды ошибкой
     (`internal/app/validateGRPCListenAddr`, см. комментарий в `grpc.go`).
3. **Sync-контур** (`sync.enabled`, интервал `sync.interval`):
   - статические пиры `sync.peers` (`addr`, опц. `node_id` — привязка к URI
     SAN серта, `host` — TLS ServerName);
   - discovery-кандидаты (mDNS) — только адреса;
   - `sync.central_url` — исходящий sync к central: TLS ServerName
     зафиксирован на central-домен (`ClientTLSConfigForCentral`), серверный
     серт проверяется `security.VerifyCentralIdentity` (hostname + серт);
     отказ — пир не central, sync не начинается.
4. **mDNS/DNS-SD discovery** (`network.mdns_enabled`): библиотека
   `github.com/hashicorp/mdns` (минимальные зависимости, поддерживает и
   регистрацию, и поиск, в отличие от заброшенного grandcat/zeroconf).
   Сервис `_przewalski-node._tcp.local`, имя инстанса = `node_id`, порт =
   gRPC. Обнаруженные адреса отдаются движку как кандидаты Peer; доверие
   НЕ по факту обнаружения — только mTLS (в plain-режиме discovered-пиры
   отсекаются вовсе).

Проекции удалённых событий: движок пишет события через `projectedStore`
(`internal/app/projections.go`: Append + проекция horse/observation/blob
в одной транзакции), поэтому `GET /api/v1/horses|observations|blobs` на
принимающей ноде видят sync-данные.

## Наблюдаемость

- прежние `/health`, `/readyz` без изменений;
- `/api/v1/node` — новый ключ `sync`: по каждому пиру
  `events_sent` / `events_received` / `last_sync_at` / `last_error` / `kind`;
- WS `/api/v1/events` — кадры `{"type":"sync_stats","peers":[...]}` после
  каждого цикла sync.

## E2E (`backend/tests/sync_e2e`, реальные интеграционные, без фейков)

Две ноды (sqlite + fsblob, реальные TCP-сокеты, mTLS; сертификаты
генерируются on the fly crypto/x509), сборка через `internal/app`:

- **(а)** лошадь+наблюдение создаются через REST на A, доезжают до B —
  проверены и события (vector_clock/event_count), и проекции (REST на B);
- **(б)** повторный sync — 0/0, без дубликатов;
- **(в)** обрыв: A останавливается и перезапускается на тех же портах —
  B возобновляет sync и получает новые события; повторный цикл пустой;
- **(г)** blob загружен по REST на A, приехал на B по gRPC, sha256 и
  содержимое совпадают; дедупликация при повторе;
- **(д)** неповеренный сертификат (чужой CA) — handshake отклонён;
- plain gRPC вне loopback запрещён; чужой node_id в handshake отклоняется
  (last_error в счётчиках, данные не едут).

## Интеграция с feat/cv-foundation (Agent H)

- `Deps.Engine` в `internal/transport/http` сохранён (rebase без конфликтов);
- `internal/app` собирает движок по новому конфигу `inference.engine`:
  `unavailable` (по умолчанию, `inference.UnavailableEngine`) или
  `reference` (`inference.ReferenceEngine` поверх fsblob);
- env-переопределение `INFER_ENGINE`.

## Проверка

`go build ./... && go vet ./... && go test ./...` — чисто; gofumpt — чисто.
