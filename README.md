# README — yc-hack-horses

Распределённая offline-first система экологического мониторинга лошадей Пржевальского.

## Состав монорепозитория
- `backend/` — Go-нода: один Sync Engine, SQLite (local) / PostgreSQL (central), REST API, gRPC sync, mTLS.
- `app/` — React + TypeScript + Vite + shadcn/ui, Feature-Sliced Design.
- `desktop/` — Tauri, Go-нода как sidecar.
- `mobile/` — React Native + Go Core bridge (foundation).
- `packages/contracts/` — общие TS-типы и API-контракты.
- `infra/` — Docker Compose, dev-сертификаты, скрипты.

## Ключевое правило
Central нода определяется **только** по hostname `yc-hack-horses.cu3rd.ru` + успешной TLS-проверке,
не по конфигу или полю role. Все межнодовые соединения — mTLS.

## Быстрый старт
- Центральная нода: `make compose-up` (см. `infra/compose`).
- Dev-сертификаты: `make dev-certs` (development-only).
- Локальная нода: `make backend && ./bin/node --config node.yaml` (см. `docs/development`).
- Frontend: `make app-install && make app-build`.

## Документация
- ADR: `docs/adr/`
- Протокол синхронизации: `backend/api/proto/sync/v1/sync.proto`
