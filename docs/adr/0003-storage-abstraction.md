# ADR-0003: SQLite/PostgreSQL storage abstraction
**Status:** accepted
## Решение
Интерфейс `storage.EventStore` (см. `backend/internal/storage/store.go`). Реализации: `internal/storage/sqlite` (modernc.org/sqlite, pure Go — нужно для мобильного embed и локального Go-сервера без CGO) и `internal/storage/postgres` (lib/pq, как в шаблоне). Обе проходят одни contract tests (`backend/internal/storage/contract/`).
## Почему
Pure Go SQLite исключает CGO из цепочки сборки mobile/desktop. Альтернатива mattn/go-sqlite3 (CGO) отклонена.
